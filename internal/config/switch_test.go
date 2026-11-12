package config

import "testing"

func TestPublishOptimize(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil || !c.PublishOptimize {
		t.Fatalf("default: %v %v", c.PublishOptimize, err)
	}
	for v, want := range map[string]bool{"off": false, "OFF": false, "0": false, "on": true, "yes": true} {
		kv := base()
		kv["PUBLISH_OPTIMIZE"] = v
		c, err := Load(env(kv))
		if err != nil || c.PublishOptimize != want {
			t.Errorf("%q: %v %v", v, c.PublishOptimize, err)
		}
	}
	kv := base()
	kv["PUBLISH_OPTIMIZE"] = "maybe"
	if _, err := Load(env(kv)); err == nil {
		t.Error("invalid value accepted")
	}
}
