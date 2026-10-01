package libopustest

var publicAPIOpusDemo HelperCache

// PublicAPIOpusDemoPath builds the pinned opus_demo with the same codec features
// and instruction lane as the public Go API. Its CLI input conversion and
// packet-file framing come directly from src/opus_demo.c.
func PublicAPIOpusDemoPath() (string, error) {
	return publicAPIOpusDemo.Path(func() (string, error) {
		return BuildPublicAPIHelper(CHelperConfig{
			Label:        "public API opus_demo",
			OutputBase:   "gopus_libopus_public_opus_demo",
			SourceFile:   "libopus_public_opus_demo.c",
			ProbeRelPath: "src/opus_demo.c",
			CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes:  []string{"src", "celt", "silk"},
			DeadStrip:    true,
		})
	})
}
