package plugins

import "testing"

func TestBuildRegistryEnabledByDefaultAndDisable(t *testing.T) {
	r, err := BuildRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("ncache"); !ok {
		t.Fatal("ncache must be registered by default")
	}
	r, _ = BuildRegistry(map[string]bool{"ncache": false})
	if _, ok := r.Get("ncache"); ok {
		t.Fatal("disabled plugin must not be registered")
	}
	if _, ok := r.Get("json"); !ok {
		t.Fatal("core profiles always present")
	}
	if _, err := BuildRegistry(map[string]bool{"nope": true}); err == nil {
		t.Fatal("unknown plugin must error")
	}
}
