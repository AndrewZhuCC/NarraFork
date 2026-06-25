package agentbasic

import "testing"

// TestAppendCodexNativeToolsImageGenerationParity locks the W840 fix: the Go
// appendCodexNativeTools must mirror the TS appendCodexNativeTools
// (server/lib/agent/openai-provider.ts:175-199):
//   - image_generation is gated on the model's input modalities
//     (supportsCodexImageGeneration), not a coarse Contains("gpt") check, so a
//     text-only codex model (gpt-5.3-codex-spark) must NOT receive it.
//   - when appended, the tool carries partial_images: 2
//     (CODEX_IMAGE_GENERATION_PARTIAL_IMAGES) and output_format: "png".
//   - an already-present image_generation tool is backfilled with defaults.
//   - useImageGeneration=false removes the tool.
func TestAppendCodexNativeToolsImageGenerationParity(t *testing.T) {
	findTool := func(tools []map[string]any, toolType string) map[string]any {
		for _, tool := range tools {
			if stringValue(tool["type"]) == toolType {
				return tool
			}
		}
		return nil
	}

	t.Run("image-capable model gets tool with partial_images and output_format", func(t *testing.T) {
		tools := appendCodexNativeTools([]map[string]any{{"type": "function", "name": "Read"}}, "gpt-5.2-codex", true, true)
		img := findTool(tools, "image_generation")
		if img == nil {
			t.Fatalf("image-capable model should receive image_generation tool, got %#v", tools)
		}
		if img["partial_images"] != codexImageGenerationPartialImages {
			t.Fatalf("partial_images should be %d, got %#v", codexImageGenerationPartialImages, img["partial_images"])
		}
		if img["output_format"] != "png" {
			t.Fatalf("output_format should be png, got %#v", img["output_format"])
		}
		if findTool(tools, "web_search") == nil {
			t.Fatalf("web_search should be present when useWebSearch=true")
		}
	})

	t.Run("provider-prefixed image-capable model also gets tool", func(t *testing.T) {
		tools := appendCodexNativeTools(nil, "codex:gpt-5.2-codex", false, true)
		if findTool(tools, "image_generation") == nil {
			t.Fatalf("bare-model lookup should treat codex:gpt-5.2-codex as image-capable, got %#v", tools)
		}
	})

	t.Run("text-only codex model does not get image_generation", func(t *testing.T) {
		tools := appendCodexNativeTools([]map[string]any{{"type": "function", "name": "Read"}}, "gpt-5.3-codex-spark", true, true)
		if findTool(tools, "image_generation") != nil {
			t.Fatalf("text-only gpt-5.3-codex-spark must NOT receive image_generation, got %#v", tools)
		}
		// web_search is unaffected by the image modality gate.
		if findTool(tools, "web_search") == nil {
			t.Fatalf("web_search should still be present for text-only model")
		}
	})

	t.Run("useImageGeneration=false removes the tool", func(t *testing.T) {
		tools := appendCodexNativeTools([]map[string]any{{"type": "image_generation", "output_format": "png"}}, "gpt-5.2-codex", false, false)
		if findTool(tools, "image_generation") != nil {
			t.Fatalf("image_generation should be removed when useImageGeneration=false, got %#v", tools)
		}
	})

	t.Run("existing image_generation tool is backfilled with defaults", func(t *testing.T) {
		tools := appendCodexNativeTools([]map[string]any{{"type": "image_generation"}}, "gpt-5.2-codex", false, true)
		img := findTool(tools, "image_generation")
		if img == nil {
			t.Fatalf("existing image_generation tool should be retained, got %#v", tools)
		}
		if img["partial_images"] != codexImageGenerationPartialImages {
			t.Fatalf("existing tool should be backfilled with partial_images=%d, got %#v", codexImageGenerationPartialImages, img["partial_images"])
		}
		if img["output_format"] != "png" {
			t.Fatalf("existing tool should be backfilled with output_format=png, got %#v", img["output_format"])
		}
	})
}

// TestSupportsCodexImageGeneration mirrors the TS supportsCodexImageGeneration
// table lookup (openai-provider.ts:128-173).
func TestSupportsCodexImageGeneration(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-5.3-codex-spark", false}, // text-only in the table
		{"gpt-5.2-codex", true},
		{"gpt-5.5", true},
		{"gpt-5.4-mini", true},
		{"codex:gpt-5.3-codex-spark", false}, // provider prefix stripped
		{"some-unknown-model", true},         // defaults to text+image
	}
	for _, tc := range cases {
		if got := supportsCodexImageGeneration(tc.model); got != tc.want {
			t.Errorf("supportsCodexImageGeneration(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}
