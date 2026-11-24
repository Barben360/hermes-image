package brief

// Recorder saves or replays source answers. A nil Recorder means live. A directory implementation comes later.
type Recorder interface {
	Save(name string, v any) error
	Load(name string, v any) error
}

// Replaying reports whether rec serves recorded answers (a --replay run). Sources that do not go
// through Env.HTTP (IMAP, the Home Assistant WebSocket) then load their recorded answers instead of
// connecting.
func Replaying(rec Recorder) bool {
	r, ok := rec.(interface{ Replaying() bool })
	return ok && r.Replaying()
}
