package tlscert

import (
	"bytes"
	"testing"
)

func TestSelfSignedIsReused(t *testing.T) {
	dir := t.TempDir()
	a, err := SelfSigned(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SelfSigned(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Certificate[0], b.Certificate[0]) {
		t.Fatal("certificate was regenerated")
	}
}
