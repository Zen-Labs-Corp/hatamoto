package twofactor

import "testing"

type legacyOmitSample struct {
	A     bool   `json:"cloneWarning,omitempty"`
	B     uint32 `json:"signCount,omitempty"`
	C     bool   `json:"keep"`
	D     []int  `json:"list"`
	Inner struct {
		E int64  `json:"publicKeyAlgorithm,omitempty"`
		F bool   `json:"credBlob,omitempty"`
		G string `json:"s"`
	} `json:"inner"`
}

func TestMarshalStoredDropsZeroOmitemptyMembers(t *testing.T) {
	var zero legacyOmitSample
	zero.Inner.G = "a&b"
	b, err := marshalStored(zero)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"keep":false,"list":null,"inner":{"s":"a\u0026b"}}`; string(b) != want {
		t.Fatalf("zero = %s, want %s", b, want)
	}
	set := legacyOmitSample{A: true, B: 7, D: []int{1}}
	set.Inner.E, set.Inner.F = -7, true
	b, err = marshalStored(set)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"cloneWarning":true,"signCount":7,"keep":false,"list":[1],"inner":{"publicKeyAlgorithm":-7,"credBlob":true,"s":""}}`; string(b) != want {
		t.Fatalf("set = %s, want %s", b, want)
	}
}
