package dred

import "testing"

func TestValidExperimentalPayload(t *testing.T) {
	valid := []byte{'D', ExperimentalVersion, 1, 2, 3, 4, 5, 6, 7, 8}
	if !ValidExperimentalPayload(valid) {
		t.Fatalf("ValidExperimentalPayload(%x)=false want true", valid)
	}

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "nil", data: nil},
		{name: "short", data: []byte{'D', ExperimentalVersion, 1, 2}},
		{name: "wrong_magic", data: []byte{'X', ExperimentalVersion, 1, 2, 3, 4, 5, 6, 7, 8}},
		{name: "wrong_version", data: []byte{'D', ExperimentalVersion + 1, 1, 2, 3, 4, 5, 6, 7, 8}},
		{name: "too_large", data: make([]byte, ExperimentalHeaderBytes+MaxDataSize+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ValidExperimentalPayload(tc.data) {
				t.Fatalf("ValidExperimentalPayload(%x)=true want false", tc.data)
			}
		})
	}
}
