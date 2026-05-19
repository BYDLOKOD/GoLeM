package cmd

import (
	"testing"
)

func TestParseFlagsTierAuto(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{"--tier", "auto", "hello"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if f.Tier != "auto" {
		t.Errorf("Tier = %q, want auto", f.Tier)
	}
	if f.Prompt != "hello" {
		t.Errorf("Prompt = %q, want hello", f.Prompt)
	}
}

func TestParseFlagsTierLight(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{"--tier", "light", "fix the typo"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if f.Tier != "light" {
		t.Errorf("Tier = %q, want light", f.Tier)
	}
}

func TestParseFlagsTierHeavy(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{"--tier", "heavy", "refactor all"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if f.Tier != "heavy" {
		t.Errorf("Tier = %q, want heavy", f.Tier)
	}
}

func TestParseFlagsTierDefaultValue(t *testing.T) {
	t.Parallel()
	f, err := ParseFlags([]string{"hello world"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if f.Tier != "" {
		t.Errorf("Tier = %q, want empty (not specified)", f.Tier)
	}
}

func TestParseFlagsTierMissingValue(t *testing.T) {
	t.Parallel()
	_, err := ParseFlags([]string{"--tier"})
	if err == nil {
		t.Fatal("expected error for --tier without value")
	}
}

func TestParseFlagsTierInvalidValue(t *testing.T) {
	t.Parallel()
	_, err := ParseFlags([]string{"--tier", "invalid", "hello"})
	if err == nil {
		t.Fatal("expected error for invalid tier value")
	}
}
