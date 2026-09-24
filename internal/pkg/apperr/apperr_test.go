package apperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodedErrors(t *testing.T) {
	sentinel := New("not_found", "job not found")
	wrapped := fmt.Errorf("load: %w", New("not_found", "other text"))
	if !errors.Is(wrapped, sentinel) {
		t.Fatal("same code must match")
	}
	if errors.Is(wrapped, New("forbidden", "x")) {
		t.Fatal("different code matched")
	}
	e, ok := As(fmt.Errorf("x: %w", New("image_count_exceeded", "too many", "max", 4)))
	if !ok || e.Code != "image_count_exceeded" || e.Params["max"] != 4 {
		t.Fatalf("As = %+v %v", e, ok)
	}
	if _, ok := As(errors.New("plain")); ok {
		t.Fatal("plain error is not coded")
	}
}
