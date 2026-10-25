package codelima

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestLimaTemplateNamesUsesReadOnlyCatalogAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "limactl")
	writeExecutable(t, path, "#!/bin/sh\n[ \"$1\" = create ] && [ \"$2\" = --list-templates ] && [ \"$#\" = 2 ] || exit 17\nprintf 'ubuntu\\ndebian\\n'\n")
	client := &LimaClient{Binary: path}
	names, err := client.TemplateNames(context.Background())
	if err != nil || !slices.Equal(names, []string{"ubuntu", "debian"}) {
		t.Fatal(names, err)
	}
	writeExecutable(t, path, "#!/bin/sh\nsleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = client.TemplateNames(ctx)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("catalog did not honor cancellation", err)
	}
}
