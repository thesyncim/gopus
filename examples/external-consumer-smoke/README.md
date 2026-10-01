# External Consumer Smoke

This separate Go module checks that downstream code can import gopus and use its
public encoder, decoder, Ogg, and RED APIs. Its local `replace` directive points
at the repository root so the checks run against the current checkout.

From this directory, run:

```sh
go test ./...
```

The tests encode and decode Opus audio, round-trip a packet through Ogg, parse an
RFC 2198 RED payload, and request packet-loss concealment through the public
decoder API. They need no audio hardware or external services.
