package plugin

import (
	"strconv"
	"strings"
	"testing"
)

const (
	annotationsKey = "annotations"
	preSyncHook    = "PreSync"
	metadataKey    = "metadata"
)

func TestApplyLifecycle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		item     renderedResource
		wantHook string
		wantWave string
		wantErr  string
	}{
		{
			name: "sync hook and cleanup",
			item: renderedResource{
				object:  map[string]any{metadataKey: map[string]any{"name": "job"}},
				hooks:   []string{preSyncHook},
				cleanup: []string{"before-next-run", "on-success"},
				wave:    -2,
				waveSet: true,
			},
			wantHook: preSyncHook,
			wantWave: "-2",
		},
		{
			name: "equivalent existing annotations",
			item: renderedResource{
				object: map[string]any{metadataKey: map[string]any{
					"name": "job",
					annotationsKey: map[string]any{
						"argocd.argoproj.io/hook":               preSyncHook,
						"argocd.argoproj.io/hook-delete-policy": "HookSucceeded,BeforeHookCreation",
					},
				}},
				hooks:   []string{preSyncHook},
				cleanup: []string{"before-next-run", "on-success"},
			},
			wantHook: preSyncHook,
		},
		{
			name:     "multiple hook phases",
			item:     renderedResource{object: map[string]any{metadataKey: map[string]any{"name": "job"}}, hooks: []string{"PostSync", preSyncHook}},
			wantHook: "PostSync,PreSync",
		},
		{
			name: "annotation conflict",
			item: renderedResource{
				object: map[string]any{metadataKey: map[string]any{
					"name":         "job",
					annotationsKey: map[string]any{"argocd.argoproj.io/hook": "Sync"},
				}},
				hooks: []string{preSyncHook},
			},
			wantErr: "conflicts",
		},
		{
			name:    "regular resource cannot be hook",
			item:    renderedResource{object: map[string]any{metadataKey: map[string]any{"name": "job"}}, hooks: []string{preSyncHook}, regular: true},
			wantErr: "both a regular resource",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := applyLifecycle(&test.item)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("applyLifecycle() error = %v, want substring %q", err, test.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatal(err)
			}
			metadata := test.item.object["metadata"].(map[string]any)
			annotations, _ := metadata["annotations"].(map[string]any)
			if got := annotations["argocd.argoproj.io/hook"]; got != test.wantHook {
				t.Errorf("hook annotation = %v, want %q", got, test.wantHook)
			}
			if test.wantWave != "" {
				if got := annotations["argocd.argoproj.io/sync-wave"]; got != test.wantWave {
					t.Errorf("wave annotation = %v, want %q", got, test.wantWave)
				}
			}
		})
	}
}

func TestAssignBootstrapWavesBeforeMainResources(t *testing.T) {
	t.Parallel()

	bootstrapZero := &renderedResource{object: map[string]any{metadataKey: map[string]any{"name": "zero"}}, bootstrap: true, bootstrapPriority: 0, bootstrapPrioritySet: true}
	bootstrapHigh := &renderedResource{object: map[string]any{metadataKey: map[string]any{"name": "high"}}, bootstrap: true, bootstrapPriority: 1001, bootstrapPrioritySet: true}
	main := &renderedResource{object: map[string]any{metadataKey: map[string]any{"name": "main", "annotations": map[string]any{"argocd.argoproj.io/sync-wave": "0"}}}, regular: true, main: true}
	resources := map[string]*renderedResource{"bootstrap-zero": bootstrapZero, "bootstrap-high": bootstrapHigh, "main": main}

	if err := assignBootstrapWaves(resources); err != nil {
		t.Fatal(err)
	}
	if bootstrapZero.wave != -2 || bootstrapHigh.wave != -1 {
		t.Fatalf("bootstrap waves = %d, %d; want -2, -1", bootstrapZero.wave, bootstrapHigh.wave)
	}
	if bootstrapZero.wave >= 0 || bootstrapHigh.wave >= 0 {
		t.Fatal("Bootstrap waves must precede a main resource at the default wave")
	}
}

func TestAssignBootstrapWavesRejectsUnrepresentableOrder(t *testing.T) {
	t.Parallel()

	bootstrap := &renderedResource{object: map[string]any{metadataKey: map[string]any{"name": "bootstrap"}}, bootstrap: true, bootstrapPrioritySet: true}
	main := &renderedResource{object: map[string]any{metadataKey: map[string]any{"name": "main", "annotations": map[string]any{"argocd.argoproj.io/sync-wave": strconv.FormatInt(-(1 << 31), 10)}}}, regular: true, main: true}
	err := assignBootstrapWaves(map[string]*renderedResource{"bootstrap": bootstrap, "main": main})
	if err == nil || !strings.Contains(err.Error(), "supported sync-wave range") {
		t.Fatalf("assignBootstrapWaves() error = %v; want sync-wave range diagnostic", err)
	}
}
