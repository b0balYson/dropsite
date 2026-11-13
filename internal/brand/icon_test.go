package brand

import (
	"bytes"
	"os"
	"testing"
)

func TestIconMatchesWebInterfaceCopy(t *testing.T) {
	ui, err := os.ReadFile("../ui/static/brand/icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Icon, ui) {
		t.Fatal("internal/brand/icon.svg differs from internal/ui/static/brand/icon.svg: copy the new icon to both")
	}
	if !bytes.HasPrefix(Icon, []byte("<svg ")) {
		t.Fatal("icon is not an SVG document")
	}
}
