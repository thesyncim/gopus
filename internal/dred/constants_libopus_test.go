package dred

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/dred/rdovae"
)

func TestDREDConstantsMatchLibopusReference(t *testing.T) {
	config := readLibopusDefines(t, "dnn", "dred_config.h")
	rdovaeConstants := readLibopusDefines(t, "dnn", "dred_rdovae_constants.h")

	for _, tc := range []struct {
		name string
		got  int
		defs map[string]string
	}{
		{name: "DRED_EXTENSION_ID", got: ExtensionID, defs: config},
		{name: "DRED_EXPERIMENTAL_VERSION", got: ExperimentalVersion, defs: config},
		{name: "DRED_EXPERIMENTAL_BYTES", got: ExperimentalHeaderBytes, defs: config},
		{name: "DRED_MIN_BYTES", got: MinBytes, defs: config},
		{name: "DRED_SILK_ENCODER_DELAY", got: SilkEncoderDelay, defs: config},
		{name: "DRED_FRAME_SIZE", got: FrameSize, defs: config},
		{name: "DRED_DFRAME_SIZE", got: DFrameSize, defs: config},
		{name: "DRED_MAX_DATA_SIZE", got: MaxDataSize, defs: config},
		{name: "DRED_ENC_Q0", got: EncQ0, defs: config},
		{name: "DRED_ENC_Q1", got: EncQ1, defs: config},
		{name: "DRED_MAX_LATENTS", got: MaxLatents, defs: config},
		{name: "DRED_NUM_REDUNDANCY_FRAMES", got: NumRedundancyFrames, defs: config},
		{name: "DRED_MAX_FRAMES", got: MaxFrames, defs: config},
		{name: "DRED_NUM_FEATURES", got: NumFeatures, defs: rdovaeConstants},
		{name: "DRED_LATENT_DIM", got: LatentDim, defs: rdovaeConstants},
		{name: "DRED_STATE_DIM", got: StateDim, defs: rdovaeConstants},
		{name: "DRED_NUM_FEATURES", got: rdovae.NumFeatures, defs: rdovaeConstants},
		{name: "DRED_LATENT_DIM", got: rdovae.LatentDim, defs: rdovaeConstants},
		{name: "DRED_STATE_DIM", got: rdovae.StateDim, defs: rdovaeConstants},
		{name: "DRED_PADDED_LATENT_DIM", got: rdovae.PaddedLatentDim, defs: rdovaeConstants},
		{name: "DRED_PADDED_STATE_DIM", got: rdovae.PaddedStateDim, defs: rdovaeConstants},
		{name: "DRED_NUM_QUANTIZATION_LEVELS", got: rdovae.NumQuantLevels, defs: rdovaeConstants},
		{name: "DRED_MAX_RNN_NEURONS", got: rdovae.MaxRNNNeurons, defs: rdovaeConstants},
		{name: "DRED_MAX_CONV_INPUTS", got: rdovae.MaxConvInputs, defs: rdovaeConstants},
		{name: "DRED_ENC_MAX_RNN_NEURONS", got: rdovae.EncMaxRNNNeurons, defs: rdovaeConstants},
		{name: "DRED_ENC_MAX_CONV_INPUTS", got: rdovae.EncMaxConvInputs, defs: rdovaeConstants},
		{name: "DRED_DEC_MAX_RNN_NEURONS", got: rdovae.DecMaxRNNNeurons, defs: rdovaeConstants},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := resolveLibopusDefineInt(t, tc.defs, tc.name, nil)
			if tc.got != want {
				t.Fatalf("%s=%d want %d", tc.name, tc.got, want)
			}
		})
	}
}

func readLibopusDefines(t *testing.T, elem ...string) map[string]string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	path := filepath.Join(append([]string{repoRoot, "tmp_check", "opus-1.6.1"}, elem...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read libopus defines from %s: %v", path, err)
	}

	defines := make(map[string]string)
	re := regexp.MustCompile(`(?m)^#define\s+([A-Za-z_][A-Za-z0-9_]*)\s+(.+?)\s*$`)
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		value := strings.TrimSpace(strings.Split(m[2], "/*")[0])
		value = strings.TrimSpace(strings.Split(value, "//")[0])
		if value != "" {
			defines[m[1]] = value
		}
	}
	return defines
}

func resolveLibopusDefineInt(t *testing.T, defs map[string]string, name string, seen map[string]bool) int {
	t.Helper()
	if seen == nil {
		seen = make(map[string]bool)
	}
	if seen[name] {
		t.Fatalf("recursive libopus define %s", name)
	}
	expr, ok := defs[name]
	if !ok {
		t.Fatalf("libopus define %s not found", name)
	}
	seen[name] = true
	defer delete(seen, name)

	identRe := regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
	expr = identRe.ReplaceAllStringFunc(expr, func(s string) string {
		if _, ok := defs[s]; !ok {
			return s
		}
		return strconv.Itoa(resolveLibopusDefineInt(t, defs, s, seen))
	})
	parsed, err := parser.ParseExpr(expr)
	if err != nil {
		t.Fatalf("parse libopus define %s=%q: %v", name, expr, err)
	}
	return evalLibopusIntExpr(t, parsed)
}

func evalLibopusIntExpr(t *testing.T, expr ast.Expr) int {
	t.Helper()
	switch e := expr.(type) {
	case *ast.BasicLit:
		v, err := strconv.Atoi(e.Value)
		if err != nil {
			t.Fatalf("invalid integer literal %q", e.Value)
		}
		return v
	case *ast.ParenExpr:
		return evalLibopusIntExpr(t, e.X)
	case *ast.UnaryExpr:
		v := evalLibopusIntExpr(t, e.X)
		switch e.Op {
		case token.ADD:
			return v
		case token.SUB:
			return -v
		default:
			t.Fatalf("unsupported unary operator %s", e.Op)
		}
	case *ast.BinaryExpr:
		left := evalLibopusIntExpr(t, e.X)
		right := evalLibopusIntExpr(t, e.Y)
		switch e.Op {
		case token.ADD:
			return left + right
		case token.SUB:
			return left - right
		case token.MUL:
			return left * right
		case token.QUO:
			return left / right
		default:
			t.Fatalf("unsupported binary operator %s", e.Op)
		}
	default:
		t.Fatalf("unsupported expression %T", expr)
	}
	return 0
}
