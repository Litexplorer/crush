package styles

import (
	"testing"
)

func TestThemeFromJSON_Dracula(t *testing.T) {
	data := []byte(`{
		"name": "Dracula",
		"id": "dracula",
		"dark": {
			"palette": {
				"neutral": "#1d1e28",
				"ink": "#f8f8f2",
				"primary": "#bd93f9",
				"accent": "#ff79c6",
				"success": "#50fa7b",
				"warning": "#ffb86c",
				"error": "#ff5555",
				"info": "#8be9fd"
			}
		}
	}`)
	styles, err := ThemeFromJSON(data)
	if err != nil {
		t.Fatalf("ThemeFromJSON failed: %v", err)
	}
	if styles.Background == nil {
		t.Fatal("Background is nil")
	}
}

func TestThemeFromJSON_Monokai(t *testing.T) {
	data := []byte(`{
		"name": "Monokai",
		"id": "monokai",
		"dark": {
			"palette": {
				"neutral": "#272822",
				"ink": "#f8f8f2",
				"primary": "#ae81ff",
				"accent": "#f92672",
				"success": "#a6e22e",
				"warning": "#fd971f",
				"error": "#f92672",
				"info": "#66d9ef"
			}
		}
	}`)
	styles, err := ThemeFromJSON(data)
	if err != nil {
		t.Fatalf("ThemeFromJSON failed: %v", err)
	}
	if styles.Background == nil {
		t.Fatal("Background is nil")
	}
}

func TestThemeFromJSON_InvalidJSON(t *testing.T) {
	_, err := ThemeFromJSON([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestThemeFromJSON_MissingPalette(t *testing.T) {
	data := []byte(`{"name": "test", "id": "test"}`)
	_, err := ThemeFromJSON(data)
	if err == nil {
		t.Fatal("expected error for missing dark palette")
	}
}
