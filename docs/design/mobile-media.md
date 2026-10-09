# Mobile image normalization

The owning implementation is `internal/mobile/media.go`. The API capability response uses its input-byte and pixel constants and its runtime format list. The service must serialize decoding; multiplying the request upload limit by the number of active requests is not a sufficient memory budget.

## Formats and transformations

PNG, JPEG and WebP are decoded by Go. HEIC/HEIF is decoded by a local `heif-dec` or `heif-convert` executable on Linux, or Apple's `sips` on macOS. `CROPTOP_HEIF_CONVERTER` can select the full path of one of those executables; `disabled` removes HEIF from advertised capabilities. Executable discovery establishes availability, not support for every codec inside a HEIF container. The Linux build includes only the libde265 HEVC decoder. AVIF, GIF, SVG, HTML, animated PNG/WebP and HEIF sequences are outside this interface.

Normalized output is an 8-bit sRGB PNG with a SHA-256 filename. It preserves image dimensions and screenshot detail, applies EXIF orientation, and omits source metadata. Re-encoding does not introduce a second JPEG compression pass. The normalizer handles the PNG EXIF orientation emitted by ImageIO when converting HEIF container rotations. A decoder that produces several image files is rejected rather than selecting the first one.

Untagged images are assumed SDR/sRGB, as are explicit standard sRGB declarations. Bounded RGB ICC profiles (including Display-P3 SDR screenshots) are color-managed before their profiles are removed. `media_color.go` extracts and validates at most 1 MiB of profile data. Go first decodes the image, applies orientation, and re-encodes only its pixels into a private PNG. The dedicated `croptop-color` helper then uses Little CMS to transform those pixels to sRGB with relative-colorimetric intent and black-point compensation, copying alpha unchanged. A final Go encoding removes all source metadata and profiles. The helper only loads this generated PNG and its separate ICC profile; it does not autodetect or load uploaded file formats. Invalid profiles cannot silently fall back to an assumed profile.

The Docker runtime includes this helper and its libpng/lcms2 libraries. Other installations can set `CROPTOP_COLOR_CONVERTER` to the helper path; `disabled` deliberately disables profiled-image processing. If color conversion is missing, the normalizer returns actionable export guidance instead of discarding the profile. Notices are included at `/usr/share/licenses/croptop-color/NOTICE` in the runtime and beside the helper source.

HDR tone mapping is **not** part of this pilot. Explicit PQ/HLG, high-bit-depth primary pixels, unrecognized gain-map encodings, non-sRGB unprofiled color declarations, CMYK JPEG, unsupported mastering-display metadata, and conflicting PNG cICP/ICC declarations still fail closed. Content-light metadata (`cLLI`) also occurs in SDR screenshots; it is validated and discarded under the same supported/assumed-SDR pixel policy, not treated as an HDR detector. Input ICC is never applied blindly to converter output; HEIF output must retain a usable color profile when its source was profiled. Missing/unspecified color metadata is treated as ordinary SDR, not proof that the coded image is SDR. This is not an exhaustive HDR detector or a claim of HDR visual fidelity. The real-device acceptance matrix still applies to the formats accepted by the pilot.

### Apple camera HEIC conversion (unreleased)

Apple camera HEIC files can contain an ordinary 8-bit SDR primary together with an HDR gain map, high-bit-depth thumbnails and grayscale semantic mattes. [Apple documents its gain map as an enhancement of the SDR base](https://developer.apple.com/documentation/appkit/applying-apple-hdr-effect-to-your-photos). These auxiliary properties must not be mistaken for the primary image's color encoding. Files containing the recognized Apple 2020 gain-map auxiliary use the small `croptop-heif` adapter, linked to the same pinned libheif/libpng libraries already in the container. The adapter selects the primary through libheif's own item APIs and validates the primary and decoded grid dependencies; Go does not implement a parallel item/property parser. The supported primary is an 8-bit HEVC image or a single-level grid of such images. Unsupported primary/dependency color encodings and alpha dependencies in this narrow path fail instead of being discarded. Other HEIF inputs retain the existing conversion path.

The adapter explicitly preserves source color encoding, geometric transforms and the primary ICC profile; the existing Little CMS stage then converts the decoded PNG to sRGB. Final normalization strips profiles and source metadata. This is **automatic HEIC input → PNG post media**, not HEIC publication and not HDR tone mapping: only the camera's SDR base is posted, and its optional HDR enhancement is omitted. The source file is untouched. The helper must be installed for this path; `CROPTOP_HEIF_PRIMARY_CONVERTER` selects its path, and `disabled` fails closed without falling back to an ambiguous conversion. The existing `CROPTOP_HEIF_CONVERTER=disabled` switch also disables this path.

Color decisions use libheif's resolved profiles and native decoded pixels, not the number or order of raw container properties. The adapter checks each coded tile before RGB conversion, rejects decoder warnings and explicit conflicting profiles, and requires actual 8-bit channels before conversion can truncate them. Equivalent color-property associations are allowed. As in the existing pilot, missing/unspecified color is assumed SDR; libheif can resolve container declarations over coded metadata, so these checks are not an exhaustive detector of hidden bitstream HDR. No second HEIF or codec-bitstream parser is maintained here.

## Resource bounds

| Resource | Limit |
|---|---:|
| Original input | 20 MiB |
| Image dimensions | 40 megapixels |
| Total normalization deadline | 30 seconds |
| Normalized PNG | 160 MiB |
| Extracted ICC profile | 1 MiB |
| Intermediate converted files | 320 MiB combined |
| Linux decoder/color-converter address space | 1 GiB |
| Linux decoder/color-converter CPU time | 30 seconds |

Dimensions are checked before standard raster decoding and again afterward. HEIF spatial extents are checked before launching the decoder; this is not a substitute for the Linux kernel limits, because a malicious coded bitstream may disagree with container metadata. The Linux child receives address-space, file-size, and CPU limits. A deadline kills its process group and bounds pipe-draining time. Intermediate files are private and removed on success or error. Diagnostic stdout/stderr is discarded to avoid recording source metadata or allowing unbounded logs.

A single 40MP image can need hundreds of MiB while decoded. The service serializes normalization. Begin the pilot with a 2 GiB container memory limit, then measure whole-service memory under real workloads before adjusting either. That container recommendation includes headroom for the publisher and is not a measured maximum. macOS rejects the address-space limit used on Linux; its development conversion path has byte, pixel, file, and time limits but no equivalent kernel address-space limit. Production untrusted-image hosting should use the Linux container.

The host's total publication limit still applies after normalization; some high-entropy images can expand substantially when converted from JPEG/HEIF to PNG. A failed publication keeps its draft and does not mean an image has been posted.

## Linux container and decoder updates

The Dockerfile builds libheif 1.23.6 from its official release archive with the release asset's SHA-256 pinned. This [security release](https://github.com/strukturag/libheif/releases/tag/v1.23.6) fixes decoder bounds issues. The build enables libde265 HEVC support and disables optional codec plugins and parallel tile decoding. The final Alpine container contains the decoder and its runtime libraries, without its compiler toolchain. Future upgrades must update both the version and verified release checksum.

Run the Linux integration gate before deployment:

```sh
docker build --platform linux/amd64 --target mobile-media-test -t croptop-mobile-media-test:local .
```

This target runs the Go media tests inside the production runtime, including a small real HEIC fixture and its 90-degree container rotation. Generated multi-item HEIC fixtures cover primary/tile selection, unused auxiliary metadata, actual native depth, decoder-resolved color conflicts, ICC preservation, cleanup and automatic PNG preparation/preview at the service boundary. It also requires the actual color helper and checks synthetic Display-P3 color patches against an independent RGB matrix calculation, including alpha, orientation, metadata removal, malformed-profile failure, profile decompression bounds, and unsupported-HDR rejection. An installed decoder or color helper that cannot actually process these fixtures fails the gate. Ordinary local tests use the same fixtures; they do not need an HEIF encoder. `go test -race ./internal/mobile` covers service concurrency separately on a suitable native build host.

Verified on 2026-10-09: the previous Docker decoder target passed on Linux/arm64, and the color-managed target passed on Linux/amd64 with libheif 1.23.6, lcms2 2.19 and libpng 1.6.59. The Apple camera conversion target and its generated primary/dependency regressions also passed. A privately supplied camera HEIC normalized to an upright 4284×5712 metadata-free PNG in 14.36 seconds under 2 CPU / 2 GiB Linux limits and the unchanged 30-second deadline; its source was unchanged and is not stored in this repository. Native macOS media regression and race tests passed with the real lcms helper and HEIC decoder. These checks do not constitute a 40MP memory benchmark, complete physical-device acceptance or HDR tone-mapping support.

## Real-device acceptance still required

Capture actual iPhone SDR PNG and HDR HEIF screenshots and Android PNG/JPEG screenshots. Compare normalized previews to their originals for readable small text, orientation/mirroring, wide-gamut colors, transparency, clipped highlights, and HDR-to-SDR appearance on Safari and Chrome. Include iCloud-sourced images, 20 MiB boundaries, 40MP boundaries, corrupted files, and timeout/retry while retaining the original draft. The embedded HEIC is a synthetic image encoded by ImageIO, not a substitute for this device acceptance.
