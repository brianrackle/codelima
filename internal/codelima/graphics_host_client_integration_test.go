//go:build cgo && (darwin || linux)

package codelima

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

type graphicsRecordingCaller struct {
	client    daemonRPCCaller
	requests  []daemon.TerminalGraphicsParams
	afterRead func()
}

func (c *graphicsRecordingCaller) Call(ctx context.Context, method string, params, result any) error {
	request, ok := params.(daemon.TerminalGraphicsParams)
	if method == "terminal.graphics" && ok {
		c.requests = append(c.requests, request)
	}
	if err := c.client.Call(ctx, method, params, result); err != nil {
		return err
	}
	if c.afterRead != nil {
		callback := c.afterRead
		c.afterRead = nil
		callback()
	}
	return nil
}

func TestGraphicsHostRouteAndFrontendFetchUseBoundedGenerationCheckedChunks(t *testing.T) {
	root := newDaemonTestRoot(t, "gfx-")
	service := NewService(DefaultConfig(filepath.Join(root, "home")), newFakeSandbox(), strings.NewReader(""), ioDiscard{}, ioDiscard{})
	if err := service.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	terminal := newIsolatedDaemonTerminalWithOptions("graphics-rpc", nil, rendererGhosttyWorkerOptions(t))
	t.Cleanup(terminal.Close)
	if err := terminal.ResizePixels(30, 8, 9, 18); err != nil {
		t.Fatal(err)
	}
	terminal.renderer = terminal.newRenderer()
	if err := terminal.renderer.Start(context.Background(), 30, 8); err != nil {
		t.Fatal(err)
	}
	pixels := bytes.Repeat([]byte{23, 117, 209, 255}, 200*100)
	var stream strings.Builder
	for offset := 0; offset < len(pixels); {
		end := min(offset+3072, len(pixels))
		more := 1
		if end == len(pixels) {
			more = 0
		}
		if offset == 0 {
			fmt.Fprintf(&stream, "\x1b_Ga=T,f=32,s=200,v=100,i=21,p=3,q=2,m=%d;", more)
		} else {
			fmt.Fprintf(&stream, "\x1b_Gq=2,m=%d;", more)
		}
		stream.WriteString(base64.StdEncoding.EncodeToString(pixels[offset:end]))
		stream.WriteString("\x1b\\")
		offset = end
	}
	if err := terminal.renderer.SendOutput(terminal.journal.AppendOutput([]byte(stream.String()))); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 5*time.Second, func() bool { return len(terminal.Snapshot().Snapshot.Graphics.Assets) == 1 }, "RPC graphics publication")
	frame := terminal.Snapshot().Snapshot.Graphics
	asset := frame.Assets[0]
	host := newDaemonHost(service)
	host.terminals["term-1"] = &daemonTerminalEntry{state: daemon.TerminalState{TerminalID: "term-1"}, term: terminal}
	server := daemon.NewServer(daemon.Config{Home: service.cfg.MetadataRoot, Version: Version, Handler: host})
	host.wireServerLinks(server)
	host.markServing()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("server.Run: %v", err)
		}
	})
	waitForDaemonPing(t, service.cfg.MetadataRoot)
	client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.Hello.InputOwner {
		t.Fatal("read-only test client unexpectedly owns input")
	}
	caller := &graphicsRecordingCaller{client: client}
	view := &daemonTUITerminal{id: "term-1", client: caller}
	data, err := fetchTUIGraphicsAsset(context.Background(), view, frame, asset)
	if err != nil || !bytes.Equal(data, pixels) {
		t.Fatalf("graphics RPC fetch bytes=%d err=%v", len(data), err)
	}
	if len(caller.requests) != 2 || caller.requests[0].Offset != 0 || caller.requests[0].Length != terminalgraphics.MaxChunkBytes || caller.requests[1].Offset != terminalgraphics.MaxChunkBytes || caller.requests[1].Length != len(pixels)-terminalgraphics.MaxChunkBytes {
		t.Fatalf("incorrect bounded chunk sequence: %+v", caller.requests)
	}
	for _, bad := range []daemon.TerminalGraphicsParams{
		{TerminalID: "term-1", RendererGeneration: frame.RendererGeneration, ImageID: asset.ImageID, Generation: asset.Generation, Length: terminalgraphics.MaxChunkBytes + 1},
		{TerminalID: "term-1", RendererGeneration: frame.RendererGeneration, ImageID: asset.ImageID, Generation: asset.Generation, Offset: -1, Length: 1},
		{TerminalID: "term-1", RendererGeneration: frame.RendererGeneration + 1, ImageID: asset.ImageID, Generation: asset.Generation, Length: 1},
		{TerminalID: "missing", RendererGeneration: frame.RendererGeneration, ImageID: asset.ImageID, Generation: asset.Generation, Length: 1},
	} {
		var chunk daemon.TerminalGraphicsChunk
		if err := client.Call(context.Background(), "terminal.graphics", bad, &chunk); err == nil {
			t.Fatalf("invalid graphics RPC accepted: %+v", bad)
		}
	}
	caller.afterRead = func() {
		if err := terminal.renderer.SendOutput(terminal.journal.AppendOutput([]byte("\x1b_Ga=d,d=I,i=21,q=2;\x1b\\"))); err != nil {
			t.Fatal(err)
		}
		waitForCondition(t, 5*time.Second, func() bool { return len(terminal.Snapshot().Snapshot.Graphics.Assets) == 0 }, "image deletion between frontend chunks")
	}
	if data, err := fetchTUIGraphicsAsset(context.Background(), view, frame, asset); err == nil || data != nil {
		t.Fatalf("partial deleted-image fetch was accepted: bytes=%d err=%v", len(data), err)
	}
}
