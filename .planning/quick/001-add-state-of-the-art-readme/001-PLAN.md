---
phase: quick-001
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - README.md
autonomous: true

must_haves:
  truths:
    - "README clearly conveys gopus is a pure Go Opus codec with no cgo"
    - "Installation instructions work with go get"
    - "Quick start examples are copy-pasteable and functional"
    - "API overview covers all public types"
    - "Feature list is comprehensive and accurate"
  artifacts:
    - path: "README.md"
      provides: "Project documentation"
      min_lines: 300
  key_links:
    - from: "README.md"
      to: "pkg.go.dev"
      via: "Badge link and API reference"
---

<objective>
Create a comprehensive, professional README for gopus - a pure Go implementation of the Opus audio codec.

Purpose: The project has completed 14 phases with 54 plans, implementing a full RFC 6716 compliant Opus encoder/decoder. It needs a README that communicates its capabilities, provides clear usage examples, and establishes credibility.

Output: README.md with badges, features, installation, quick start, API overview, examples, benchmarks placeholder, contributing, and license sections.
</objective>

<execution_context>
@~/.claude/get-shit-done/workflows/execute-plan.md
@~/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@doc.go
@encoder.go
@decoder.go
@stream.go
@multistream.go
@container/ogg/doc.go
</context>

<tasks>

<task type="auto">
  <name>Task 1: Create comprehensive README.md</name>
  <files>README.md</files>
  <action>
Create a state-of-the-art README.md with the following structure:

**Header section:**
- Project name with Go gopher or audio-related visual concept (text-based, no emojis)
- One-line description: "Pure Go implementation of the Opus audio codec"
- Badges: Go Reference (pkg.go.dev), Go Report Card, License (MIT or appropriate), Build Status placeholder

**Key highlights box:**
- No CGO, no external dependencies
- RFC 6716 (Opus) and RFC 7845 (Ogg Opus) compliant
- Full encode/decode support
- SILK, CELT, and Hybrid modes
- Mono/stereo + surround sound (up to 7.1)

**Features section:**
Organize as a feature matrix or categorized list:
- Decoder: All modes (SILK/CELT/Hybrid), all bandwidths (NB to FB), all frame sizes (2.5-60ms), PLC, multistream
- Encoder: VBR/CBR/CVBR, FEC, DTX, complexity control (0-10), all application hints
- Container: Ogg Opus read/write per RFC 7845
- Streaming: io.Reader/io.Writer wrappers, PacketSource/PacketSink interfaces
- Multistream: 1-8 channels, Vorbis-style channel mapping

**Installation:**
```
go get gopus
```

**Quick Start section:**
Copy the examples from doc.go for:
- Basic encoding (float32)
- Basic decoding (float32)
- Include notes about frame sizes (960 samples = 20ms at 48kHz)

**API Overview section:**
Table or structured list of public types:
| Type | Purpose |
|------|---------|
| Encoder | Frame-based encoding |
| Decoder | Frame-based decoding |
| MultistreamEncoder | Surround sound encoding |
| MultistreamDecoder | Surround sound decoding |
| Reader | io.Reader for streaming decode |
| Writer | io.Writer for streaming encode |
| container/ogg.Reader | Ogg Opus file reading |
| container/ogg.Writer | Ogg Opus file writing |

**Advanced Usage section:**
- Encoder configuration (bitrate, complexity, FEC, DTX)
- Multistream example for 5.1 surround
- Ogg container example
- Streaming API example

**Supported Configurations:**
- Sample rates: 8000, 12000, 16000, 24000, 48000 Hz
- Channels: 1 (mono), 2 (stereo), up to 8 for multistream
- Frame sizes: 2.5, 5, 10, 20, 40, 60 ms
- Bitrates: 6-510 kbps

**Benchmarks section:**
Placeholder with "Coming soon" or basic structure for future benchmarks.
Note: Pure Go implementation focuses on correctness; performance optimizations ongoing.

**Comparison with libopus:**
Brief note that this is a pure Go implementation, not a cgo wrapper.
Mention compatibility: packets are interoperable with libopus.

**Contributing section:**
Standard contributing guidelines pointing to issues/PRs.

**License section:**
MIT license (or check if project has a LICENSE file).

**Acknowledgments:**
- RFC 6716, RFC 7845, RFC 8251
- Xiph.Org Foundation for Opus codec specification
  </action>
  <verify>
- File exists: README.md
- Contains all major sections: Features, Installation, Quick Start, API Overview
- Badges are properly formatted markdown
- Code examples use proper Go syntax
- No broken markdown formatting
- `cat README.md | wc -l` shows 300+ lines
  </verify>
  <done>
README.md exists with comprehensive documentation covering all public APIs, installation instructions, quick start examples, and proper markdown formatting with badges.
  </done>
</task>

<task type="auto">
  <name>Task 2: Verify README accuracy and completeness</name>
  <files>README.md</files>
  <action>
Validate the README content:

1. Verify all API types mentioned exist in the codebase:
   - Check encoder.go exports: Encoder, NewEncoder, Application constants
   - Check decoder.go exports: Decoder, NewDecoder
   - Check multistream.go exports: MultistreamEncoder, MultistreamDecoder
   - Check stream.go exports: Reader, Writer, PacketSource, PacketSink
   - Check container/ogg: Reader, Writer

2. Verify code examples are syntactically correct:
   - Run `gofmt -e` on extracted code blocks (mental check)
   - Ensure import paths are correct (gopus, gopus/container/ogg)

3. Verify feature claims are accurate based on STATE.md:
   - 54 plans completed
   - All modes implemented (SILK, CELT, Hybrid)
   - Multistream supports 1-8 channels
   - FEC, DTX, VBR/CBR/CVBR all implemented

4. Check for any LICENSE file and update License section accordingly:
   - If no LICENSE exists, note "License TBD" or use MIT as placeholder
  </action>
  <verify>
- All mentioned types exist in codebase (grep confirms)
- No claims about features that don't exist
- License section reflects actual project license status
  </verify>
  <done>
README accurately represents the project's capabilities without overstating features. All API references point to real exports.
  </done>
</task>

</tasks>

<verification>
- README.md exists in repository root
- All markdown renders correctly (no broken links, proper code blocks)
- Quick start examples are complete and runnable
- Feature list matches actual implementation (verified against STATE.md)
- Badges use correct URLs for gopus module path
</verification>

<success_criteria>
- README.md created with 300+ lines of content
- All major sections present: badges, features, installation, quick start, API, examples
- Code examples are syntactically correct Go
- No claims about unimplemented features
- Professional presentation suitable for open source project
</success_criteria>

<output>
After completion, create `.planning/quick/001-add-state-of-the-art-readme/001-SUMMARY.md`
</output>
