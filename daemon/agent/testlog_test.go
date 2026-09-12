package agent

import "log/slog"

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError}))
}
