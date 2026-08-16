package application

import "context"

// BodyCodecRow is a single unknown-codec observation from the events table.
// Count is the number of rows carrying that codec value; SampleIDs holds up
// to a bounded number of event IDs so an operator can locate the rows.
type BodyCodecRow struct {
	Codec     string
	Count     int64
	SampleIDs []string
}

// BodyCodecState holds the result of scanning events.body_codec for values
// the current binary cannot decode.
type BodyCodecState struct {
	UnknownRows []BodyCodecRow
}

// BodyCodecChecker reports events rows whose body_codec value the running
// binary's decoder does not support.
type BodyCodecChecker interface {
	CheckBodyCodec(context.Context) (BodyCodecState, error)
}
