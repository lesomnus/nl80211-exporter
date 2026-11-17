package nl80211receiver

var (
	ToMetrics = toMetrics
	NewStarts = newStarts
)

// Reader is what a receiver reads through.
type Reader = reader

// SetOpen makes the receivers created after it open readers with f, until restore.
func SetOpen(f func() (Reader, error)) (restore func()) {
	prev := open
	open = f
	return func() { open = prev }
}
