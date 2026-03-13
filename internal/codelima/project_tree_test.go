package codelima

import (
	"context"
	"path/filepath"
	"testing"
)

func TestProjectTreeIncludesProjectNodes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, workspace := newTestService(t)
	writeFile(t, filepath.Join(workspace, "README.md"), "hello\n")

	root, err := service.ProjectCreate(ctx, ProjectCreateInput{
		Slug:          "root",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("ProjectCreate(root) error = %v", err)
	}

	if _, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: root.ID,
		Slug:    "root-node",
	}); err != nil {
		t.Fatalf("NodeCreate(root-node) error = %v", err)
	}

	childWorkspace := filepath.Join(t.TempDir(), "child")
	child, err := service.ProjectFork(ctx, ProjectForkInput{
		SourceProject: root.ID,
		Slug:          "child",
		WorkspacePath: childWorkspace,
	})
	if err != nil {
		t.Fatalf("ProjectFork(child) error = %v", err)
	}

	if _, err := service.NodeCreate(ctx, NodeCreateInput{
		Project: child.ID,
		Slug:    "child-node",
	}); err != nil {
		t.Fatalf("NodeCreate(child-node) error = %v", err)
	}

	tree, err := service.ProjectTree("", false)
	if err != nil {
		t.Fatalf("ProjectTree() error = %v", err)
	}

	if len(tree) != 1 {
		t.Fatalf("expected 1 root tree node, got %d", len(tree))
	}

	if len(tree[0].Nodes) != 1 || tree[0].Nodes[0].Slug != "root-node" {
		t.Fatalf("expected root project nodes to include root-node, got %+v", tree[0].Nodes)
	}

	if len(tree[0].Children) != 1 {
		t.Fatalf("expected root project to have 1 child, got %d", len(tree[0].Children))
	}

	if len(tree[0].Children[0].Nodes) != 1 || tree[0].Children[0].Nodes[0].Slug != "child-node" {
		t.Fatalf("expected child project nodes to include child-node, got %+v", tree[0].Children[0].Nodes)
	}
}

func TestRenderProjectTreeIncludesNodeLeaves(t *testing.T) {
	t.Parallel()

	tree := []ProjectTreeNode{
		{
			Project: Project{Slug: "root"},
			Nodes: []Node{
				{Slug: "root-node"},
			},
			Children: []ProjectTreeNode{
				{
					Project: Project{Slug: "child"},
					Nodes: []Node{
						{Slug: "child-node"},
					},
				},
			},
		},
	}

	output := renderProjectTree(tree, "")
	expected := "" +
		"└── root\n" +
		"    ├── node: root-node\n" +
		"    └── child\n" +
		"        └── node: child-node\n"

	if output != expected {
		t.Fatalf("expected rendered tree %q, got %q", expected, output)
	}
}
