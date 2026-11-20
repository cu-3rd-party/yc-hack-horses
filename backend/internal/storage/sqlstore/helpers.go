package sqlstore

import (
	"time"
)

func timeFromUnixMilli(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func timeNowUTC() time.Time { return time.Now().UTC() }
