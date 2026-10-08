package web

import (
	"testing"
	"time"
)

func TestBytesText(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 bytes", 1: "1 byte", 812: "812 bytes", 1024: "1 KB", 4300: "4.2 KB",
		3 << 20: "3 MB", 38*(1<<30) + 1<<29: "38.5 GB", 20 << 40: "20 TB", 3 << 50: "3072 TB",
	} {
		if got := bytesText(n); got != want {
			t.Errorf("bytesText(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestMoney(t *testing.T) {
	tests := []struct {
		currency string
		v        float64
		want     string
	}{
		{"EUR", 4.7481, "€4.75"},
		{"EUR", 0, "€0.00"},
		{"USD", 12.5, "$12.50"},
		{"CHF", 3.333, "3.33 CHF"},
		{"", 1, "1.00"},
	}
	for _, tt := range tests {
		if got := money(tt.currency, tt.v); got != tt.want {
			t.Errorf("money(%q, %v) = %q, want %q", tt.currency, tt.v, got, tt.want)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "less than a minute", time.Minute: "1 minute", 59 * time.Minute: "59 minutes",
		3 * time.Hour: "3 hours", 47 * time.Hour: "47 hours", 50 * time.Hour: "2 days",
	} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q, want %q", d, got, want)
		}
	}
}
