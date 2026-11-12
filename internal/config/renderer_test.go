package config

import "testing"

func TestRendererSettings(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Renderer.Enabled() || c.Renderer.Addr != ":8083" || c.Renderer.BaseURL != "" {
		t.Fatalf("defaults %+v", c.Renderer)
	}
	kv := base()
	kv["RENDERER_URL"] = "http://renderer:3000/"
	if c, err = Load(env(kv)); err != nil {
		t.Fatal(err)
	}
	if !c.Renderer.Enabled() || c.Renderer.URL != "http://renderer:3000" || c.Renderer.BaseURL != "http://control:8083" {
		t.Fatalf("enabled %+v", c.Renderer)
	}
	kv["RENDER_BASE_URL"] = "http://10.0.0.2:9000"
	kv["RENDER_ADDR"] = ":9000"
	if c, err = Load(env(kv)); err != nil || c.Renderer.BaseURL != "http://10.0.0.2:9000" || c.Renderer.Addr != ":9000" {
		t.Fatalf("overrides %+v %v", c.Renderer, err)
	}
	kv["RENDERER_URL"] = "renderer:3000"
	if _, err := Load(env(kv)); err == nil {
		t.Fatal("URL without scheme accepted")
	}
}
