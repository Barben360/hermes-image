package brief

// Recorder saves or replays source answers. A nil Recorder means live. A directory implementation comes later.
type Recorder interface {
	Save(name string, v any) error
	Load(name string, v any) error
}
