package entity

import (
	"encoding/json/jsontext"
	"reflect"
	"sync"
	"testing"
)

func TestFlatAttributesDecodesOnce(t *testing.T) {
	e := &Entity{Attributes: jsontext.Value(`{"type":"x","n":2,"b":true,"o":{}}`)}
	m1 := e.FlatAttributes()
	if want := FlattenAttributes(e.Attributes); !reflect.DeepEqual(m1, want) {
		t.Fatalf("got %v want %v", m1, want)
	}
	if reflect.ValueOf(m1).Pointer() != reflect.ValueOf(e.FlatAttributes()).Pointer() {
		t.Fatal("repeat call returned a different map")
	}
	if n := testing.AllocsPerRun(100, func() { _ = e.FlatAttributes() }); n != 0 {
		t.Fatalf("allocs per call = %v, want 0", n)
	}
}

func TestFlatAttributesFollowsReplacedAttributes(t *testing.T) {
	e := &Entity{Attributes: jsontext.Value(`{"type":"x"}`)}
	if e.FlatAttributes()["type"] != "x" {
		t.Fatal("want x")
	}
	e.Attributes = jsontext.Value(`{"type":"y"}`)
	if e.FlatAttributes()["type"] != "y" {
		t.Fatal("want y after replace")
	}
	e.Attributes = DerivedAttributes(e.Attributes)
	if got := e.FlatAttributes()["type"]; got != "y" {
		t.Fatalf("after DerivedAttributes got %q, want y", got)
	}
	c := *e
	c.Attributes = jsontext.Value(`{"type":"z"}`)
	if c.FlatAttributes()["type"] != "z" {
		t.Fatal("copy want z")
	}
	if e.FlatAttributes()["type"] != "y" {
		t.Fatal("original want y")
	}
	e.Attributes = nil
	if e.FlatAttributes() != nil {
		t.Fatal("nil attributes want nil map")
	}
	e.Attributes = jsontext.Value{}
	if e.FlatAttributes() != nil {
		t.Fatal("empty attributes want nil map")
	}
}

func TestFlatAttributesConcurrent(t *testing.T) {
	e := &Entity{Attributes: jsontext.Value(`{"type":"x","n":2}`)}
	want := FlattenAttributes(e.Attributes)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				if got := e.FlatAttributes(); !reflect.DeepEqual(got, want) {
					t.Errorf("got %v want %v", got, want)
					return
				}
			}
		})
	}
	wg.Wait()
}
