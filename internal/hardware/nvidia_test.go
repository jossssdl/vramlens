package hardware

import "testing"

func TestParseNvidiaSMI(t *testing.T) {
	gpus, err := parseNvidiaSMI("NVIDIA GeForce RTX 5060 Laptop GPU, 8151, 7778\nNVIDIA A100, SXM4, 40960, 40000\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 2 {
		t.Fatalf("got %d gpus", len(gpus))
	}
	if gpus[0].VRAMTotal != 8151*mib || gpus[0].VRAMFree != 7778*mib {
		t.Errorf("bad memory: %+v", gpus[0])
	}
	if gpus[1].Name != "NVIDIA A100, SXM4" {
		t.Errorf("name with comma: %q", gpus[1].Name)
	}
	if _, err := parseNvidiaSMI("garbage"); err == nil {
		t.Error("expected error")
	}
}
