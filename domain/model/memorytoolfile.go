package model

import (
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// MemoryToolFile is a filesystem-shaped file used by Anthropic's native
// memory tool. It is intentionally separate from Traceary's durable Memory
// aggregate.
type MemoryToolFile struct {
	path      types.MemoryToolPath
	content   []byte
	createdAt time.Time
	updatedAt time.Time
}

// NewMemoryToolFile constructs a new memory-tool file.
func NewMemoryToolFile(path types.MemoryToolPath, content []byte, now time.Time) *MemoryToolFile {
	return &MemoryToolFile{
		path:      path,
		content:   append([]byte(nil), content...),
		createdAt: now,
		updatedAt: now,
	}
}

// MemoryToolFileOf restores a memory-tool file from persisted values.
func MemoryToolFileOf(path types.MemoryToolPath, content []byte, createdAt time.Time, updatedAt time.Time) *MemoryToolFile {
	return &MemoryToolFile{
		path:      path,
		content:   append([]byte(nil), content...),
		createdAt: createdAt,
		updatedAt: updatedAt,
	}
}

// WithContent returns a copy with updated content and timestamp.
func (f *MemoryToolFile) WithContent(content []byte, updatedAt time.Time) *MemoryToolFile {
	return &MemoryToolFile{
		path:      f.path,
		content:   append([]byte(nil), content...),
		createdAt: f.createdAt,
		updatedAt: updatedAt,
	}
}

// WithPath returns a copy with a new path and updated timestamp.
func (f *MemoryToolFile) WithPath(path types.MemoryToolPath, updatedAt time.Time) *MemoryToolFile {
	return &MemoryToolFile{
		path:      path,
		content:   append([]byte(nil), f.content...),
		createdAt: f.createdAt,
		updatedAt: updatedAt,
	}
}

// Path returns the canonical memory-tool file path.
func (f *MemoryToolFile) Path() types.MemoryToolPath { return f.path }

// Content returns a defensive copy of the file content.
func (f *MemoryToolFile) Content() []byte { return append([]byte(nil), f.content...) }

// SizeBytes returns the content size in bytes.
func (f *MemoryToolFile) SizeBytes() int64 { return int64(len(f.content)) }

// CreatedAt returns when the file was created.
func (f *MemoryToolFile) CreatedAt() time.Time { return f.createdAt }

// UpdatedAt returns when the file was last updated.
func (f *MemoryToolFile) UpdatedAt() time.Time { return f.updatedAt }
