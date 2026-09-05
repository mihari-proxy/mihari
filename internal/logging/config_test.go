package logging

import (
	"log/slog"
	"testing"
)

func TestParseLevel_RecognizesSupportedLevels(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: "info", want: slog.LevelInfo},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseLevel(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ParseLevel(%q)=%v want %v", test.input, got, test.want)
			}
		})
	}
}

func TestParseLevel_RejectsUnsupportedLevel(t *testing.T) {
	if _, err := ParseLevel("INFO"); err == nil {
		t.Fatal("ParseLevel accepted unsupported level")
	}
}

func TestConfigFromFields_ConvertsValidatedFields(t *testing.T) {
	got, err := ConfigFromFields("warn", 7, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Level: slog.LevelWarn, MaxSizeBytes: 7 * 1024 * 1024, MaxFiles: 4}
	if got != want {
		t.Fatalf("ConfigFromFields()=%+v want %+v", got, want)
	}
}

func TestConfigFromFields_AcceptsInclusiveBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		maxSizeMB int64
		maxFiles  int64
		want      Config
	}{
		{name: "minimum", maxSizeMB: 1, maxFiles: 1, want: Config{Level: slog.LevelInfo, MaxSizeBytes: 1 << 20, MaxFiles: 1}},
		{name: "maximum", maxSizeMB: 100, maxFiles: 10, want: Config{Level: slog.LevelInfo, MaxSizeBytes: 100 << 20, MaxFiles: 10}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ConfigFromFields("info", test.maxSizeMB, test.maxFiles)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ConfigFromFields()=%+v want %+v", got, test.want)
			}
		})
	}
}

func TestConfigFromFields_RejectsOutOfRangeLimits(t *testing.T) {
	tests := []struct {
		name      string
		maxSizeMB int64
		maxFiles  int64
	}{
		{name: "zero size", maxSizeMB: 0, maxFiles: 3},
		{name: "oversize", maxSizeMB: 101, maxFiles: 3},
		{name: "zero files", maxSizeMB: 10, maxFiles: 0},
		{name: "too many files", maxSizeMB: 10, maxFiles: 11},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ConfigFromFields("info", test.maxSizeMB, test.maxFiles); err == nil {
				t.Fatal("ConfigFromFields accepted invalid limits")
			}
		})
	}
}
