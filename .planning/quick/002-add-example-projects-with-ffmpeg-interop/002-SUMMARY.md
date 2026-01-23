# Quick Task 002: Add Example Projects with FFmpeg Interop

## Summary

Created practical example programs demonstrating gopus usage patterns with ffmpeg interoperability and roundtrip validation.

## Deliverables

### 1. examples/ffmpeg-interop/main.go
- Generates stereo 440Hz test tones
- Encodes to Ogg Opus using gopus
- Prints verification commands for ffprobe/ffplay
- Can decode ffmpeg-encoded files

### 2. examples/roundtrip/main.go
- Comprehensive encode-decode quality validation
- Multiple test signals: sine, sweep, noise, speech
- Quality metrics: SNR, correlation, peak error, MSE
- Configurable bitrate, channels, duration

### 3. examples/ogg-file/main.go
- Creates Ogg Opus files with test audio
- Reads and analyzes Ogg Opus files
- Reports file statistics, duration, compression ratio

### 4. examples/README.md
- Complete documentation for all examples
- Build instructions
- Usage examples with expected output
- API overview for common operations

## Commits

- `f7b2cae` - feat(quick-002): add ffmpeg-interop example
- `e8668eb` - feat(quick-002): add roundtrip validation example
- `3aaf2f2` - feat(quick-002): add ogg-file example and examples README

## Usage

```bash
# Build all examples
go build ./examples/...

# FFmpeg interop
./examples/ffmpeg-interop/ffmpeg-interop -out output.opus -duration 2
ffprobe output.opus

# Roundtrip validation
./examples/roundtrip/roundtrip -all

# Ogg file creation/reading
./examples/ogg-file/ogg-file -out podcast.opus -duration 5
```

## Files Created

| File | Lines | Purpose |
|------|-------|---------|
| examples/ffmpeg-interop/main.go | ~200 | FFmpeg interop demo |
| examples/roundtrip/main.go | ~300 | Quality validation |
| examples/ogg-file/main.go | ~230 | Ogg file I/O |
| examples/README.md | 208 | Documentation |
