package unpixel_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire DefaultComponents
)

func TestVerify_decisive(t *testing.T) {
	img := loadFixtureImage(t, "block08_go.png")
	vs, err := unpixel.Verify(t.Context(), img, []string{"go", "xy"},
		unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz "),
		unpixel.WithBlockSize(8),
		unpixel.WithMaxLength(3))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	byText := map[string]unpixel.Verdict{}
	for _, v := range vs {
		byText[v.Text] = v
	}
	if got := byText["go"]; !got.Match || got.Distance > 0.2 {
		t.Errorf("Verify(go) = {dist %.3f, match %v}, want match with dist≈0", got.Distance, got.Match)
	}
	if got := byText["xy"]; got.Match {
		t.Errorf("Verify(xy) = match (dist %.3f), want no-match for a wrong string", got.Distance)
	}
}
