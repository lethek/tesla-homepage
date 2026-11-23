package store

import "time"

// iconGrace is how long a new, unreferenced icon survives cleanup.
var iconGrace = 5 * time.Minute

func recent(t time.Time) bool { return time.Since(t) < iconGrace }
