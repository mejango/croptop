# Mobile image normalization

The owning implementation is `internal/mobile/media.go`. The API capability response uses its input-byte and pixel constants and its runtime format list. The service must serialize decoding; multiplying the request upload limit by the number of active requests is not a sufficient memory budget.

## Formats and transformations

PNG, JPEG and WebP are decoded by Go. HEIC/HEIF is decoded by a local `heif-dec` or `heif-convert` executable on Linux, or Apple's `sips` on macOS. `CROPTOP_HEIF_CONVERTER` can select the full path of one of those executables; `disabled` removes HEIF from advertised capabilities. Executable discovery establishes availability, not support for every codec inside a HEIF container. The Linux build includes only the libde265 HEVC decoder. AVIF, GIF, SVG, HTML, animated PNG/WebP and HEIF sequences are outside this interface.

Normalized output is an 8-bit PNG with a SHA-256 filename. It preserves image dimensions and screenshot detail, applies EXIF orientation, and omits source metadata. Re-encoding does not introduce a second JPEG compression pass. The normalizer handles the PNG EXIF orientation emitted by ImageIO when converting HEIF container rotations. A decoder that produces several image files is rejected rather than selecting the first one.

Color fidelity remains a release gate: Go's raster decoders do not perform ICC color management. Removing a Display-P3 or other non-sRGB profile can change displayed colors. Sixteen-bit channels are reduced to eight bits. The macOS HEIF path requests ImageIO's sharing color optimization, but the Linux libheif path does not guarantee an equivalent HDR-to-SDR tone map. The current tests establish decoding, geometry, metadata removal, and ordinary unprofiled screenshot pixels; they do not establish HDR or wide-gamut fidelity. Do not advertise complete iPhone HDR support until the real-device fixture matrix below passes and any necessary color conversion is implemented.

## Resource bounds

| Resource | Limit |
|---|---:|
| Original input | 20 MiB |
| Image dimensions | 40 megapixels |
| Total normalization deadline | 30 seconds |
| Normalized PNG | 160 MiB |
| Intermediate converted files | 320 MiB combined |
| Linux converter address space | 1 GiB |
| Linux converter CPU time | 30 seconds |

Dimensions are checked before standard raster decoding and again afterward. HEIF spatial extents are checked before launching the decoder; this is not a substitute for the Linux kernel limits, because a malicious coded bitstream may disagree with container metadata. The Linux child receives address-space, file-size, and CPU limits. A deadline kills its process group and bounds pipe-draining time. Intermediate files are private and removed on success or error. Diagnostic stdout/stderr is discarded to avoid recording source metadata or allowing unbounded logs.

A single 40MP image with 16-bit channels can need about 320 MB for its decoded pixels. Begin the pilot with one active normalization and a 2 GiB container memory limit, then measure whole-service memory under real workloads before adjusting either. That container recommendation includes headroom for the publisher and is not a measured maximum. macOS rejects the address-space limit used on Linux; its development conversion path has byte, pixel, file, and time limits but no equivalent kernel address-space limit. Production untrusted-image hosting should use the Linux container.

The host's total publication limit still applies after normalization; some high-entropy images can expand substantially when converted from JPEG/HEIF to PNG. A failed publication keeps its draft and does not mean an image has been posted.

## Linux container and decoder updates

The Dockerfile builds libheif 1.23.6 from its official release archive with the release asset's SHA-256 pinned. This [security release](https://github.com/strukturag/libheif/releases/tag/v1.23.6) fixes decoder bounds issues. The build enables libde265 HEVC support and disables optional codec plugins and parallel tile decoding. The final Alpine container contains the decoder and its runtime libraries, without its compiler toolchain. Future upgrades must update both the version and verified release checksum.

Run the Linux integration gate before deployment:

```sh
docker build --target mobile-media-test -t croptop-mobile-media-test:local .
```

This target runs the Go media tests inside the production runtime, including a small real HEIC fixture and its 90-degree container rotation. An installed decoder that cannot actually decode that fixture fails the gate. Ordinary local tests use the same fixture; they do not need an HEIF encoder. `go test -race ./internal/mobile` covers service concurrency separately on a suitable native build host.

Verified on 2026-10-09: the Docker target built and passed on Linux/arm64 with libheif 1.23.6. The latest media test binary also passed in that runtime with networking disabled, a 2 GiB memory cap, and a 128-process cap, including the test that checks the decoder child receives its address-space and CPU limits. Native macOS media regression and race tests passed. These small-fixture checks do not constitute a 40MP memory benchmark or HDR acceptance.

## Real-device acceptance still required

Capture actual iPhone SDR PNG and HDR HEIF screenshots and Android PNG/JPEG screenshots. Compare normalized previews to their originals for readable small text, orientation/mirroring, wide-gamut colors, transparency, clipped highlights, and HDR-to-SDR appearance on Safari and Chrome. Include iCloud-sourced images, 20 MiB boundaries, 40MP boundaries, corrupted files, and timeout/retry while retaining the original draft. The embedded HEIC is a synthetic image encoded by ImageIO, not a substitute for this device acceptance.
