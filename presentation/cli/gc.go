package cli

import "time"

const defaultRetentionDays = 90

var gcNowFunc = time.Now
