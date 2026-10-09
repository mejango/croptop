/* Croptop's primary-image-only HEIF adapter. libheif owns container parsing;
 * this adapter validates the primary and its direct coded grid dependencies.
 * Decode that exact item, retain its ICC for the separate color transform, and
 * write only RGBA pixels plus that ICC. Never export auxiliary images, EXIF,
 * XMP, names, or gain maps. libheif and libpng are dynamically linked. */
#include <libheif/heif.h>
#include <libheif/heif_items.h>
#include <png.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MAX_TILES 4096

typedef struct {
    uint8_t *icc;
    size_t icc_size;
    heif_color_profile_nclx *nclx;
} image_profile;

static void free_profile(image_profile *profile) {
    free(profile->icc);
    if (profile->nclx) heif_nclx_color_profile_free(profile->nclx);
    memset(profile, 0, sizeof(*profile));
}

static int number(const char *value, uint32_t *out) {
    if (!value || !*value) return 0;
    for (const char *p = value; *p; ++p) {
        if (*p < '0' || *p > '9') return 0;
    }
    char *end = NULL;
    errno = 0;
    unsigned long result = strtoul(value, &end, 10);
    if (errno || !end || *end || !result || result > UINT32_MAX) return 0;
    *out = (uint32_t)result;
    return 1;
}

static int dimensions(int width, int height, uint32_t max_pixels) {
    return width > 0 && height > 0 &&
        (uint64_t)width * (uint64_t)height <= max_pixels;
}

static int sdr_transfer(int transfer) {
    return transfer != heif_transfer_characteristic_ITU_R_BT_2100_0_PQ &&
        transfer != heif_transfer_characteristic_ITU_R_BT_2100_0_HLG;
}

static int unspecified_color(const heif_color_profile_nclx *nclx) {
    return nclx->color_primaries == heif_color_primaries_unspecified &&
        nclx->transfer_characteristics == heif_transfer_characteristic_unspecified;
}

/* An ICC is authoritative for the rendered image, while NCLX also specifies
 * how the decoder reconstructs RGB from YCbCr. Never borrow a different tile
 * ICC or a conflicting declared tile NCLX for the primary image. */
static int compatible_profile(const image_profile *primary, const image_profile *tile) {
    if (tile->icc_size && (tile->icc_size != primary->icc_size ||
        memcmp(tile->icc, primary->icc, tile->icc_size) != 0)) return 0;
    if (!tile->nclx) return 1;
    if (!primary->nclx) return unspecified_color(tile->nclx);
    return tile->nclx->color_primaries == primary->nclx->color_primaries &&
        tile->nclx->transfer_characteristics == primary->nclx->transfer_characteristics &&
        tile->nclx->matrix_coefficients == primary->nclx->matrix_coefficients &&
        tile->nclx->full_range_flag == primary->nclx->full_range_flag;
}

static int read_profile(const heif_image_handle *handle,
                        uint32_t max_pixels, uint32_t max_profile, image_profile *profile) {
    if (!dimensions(heif_image_handle_get_width(handle),
                    heif_image_handle_get_height(handle), max_pixels) ||
        heif_image_handle_get_luma_bits_per_pixel(handle) != 8 ||
        heif_image_handle_get_chroma_bits_per_pixel(handle) != 8 ||
        heif_image_handle_has_alpha_channel(handle) ||
        heif_image_handle_is_premultiplied_alpha(handle)) return 0;

    profile->icc_size = heif_image_handle_get_raw_color_profile_size(handle);
    if (profile->icc_size > max_profile) return 0;
    if (profile->icc_size) {
        heif_color_profile_type type = heif_image_handle_get_color_profile_type(handle);
        if (type != heif_color_profile_type_prof && type != heif_color_profile_type_rICC) return 0;
        profile->icc = malloc(profile->icc_size);
        if (!profile->icc) return 0;
        if (heif_image_handle_get_raw_color_profile(handle, profile->icc).code != heif_error_Ok) return 0;
    }
    heif_error error = heif_image_handle_get_nclx_color_profile(handle, &profile->nclx);
    if (error.code != heif_error_Ok && error.code != heif_error_Color_profile_does_not_exist) return 0;
    if (error.code == heif_error_Ok && !profile->nclx) return 0;
    if (profile->nclx && !sdr_transfer(profile->nclx->transfer_characteristics)) return 0;

    /* Use the decoder's resolved profiles. libheif can add equivalent color
     * property associations while reading, so its property count is not a
     * reliable duplicate-input check. The same resolved profiles drive decode;
     * native decoded metadata and warnings are checked before conversion. */
    return 1;
}

/* The decoded profile can include information from the bitstream that was not
 * present in the container. Do not discard a newly discovered HDR transfer or
 * emit an unprofiled PNG for pixels outside the caller's assumed-SDR policy. */
static int decoded_color(const heif_image *image, const image_profile *primary) {
    heif_color_profile_nclx *nclx = NULL;
    heif_error error = heif_image_get_nclx_color_profile(image, &nclx);
    if (error.code == heif_error_Color_profile_does_not_exist) return 1;
    if (error.code != heif_error_Ok || !nclx) return 0;
    int primaries = nclx->color_primaries;
    int transfer = nclx->transfer_characteristics;
    heif_nclx_color_profile_free(nclx);
    if (!sdr_transfer(transfer)) return 0;
    if (primary->nclx) {
        /* RGB reconstruction may change matrix/range, but must not change
         * known primaries or transfer. Unspecified tile values can inherit
         * the primary's declared color; explicit conflicts cannot. */
        if ((primaries != heif_color_primaries_unspecified &&
             primaries != (int)primary->nclx->color_primaries) ||
            (transfer != heif_transfer_characteristic_unspecified &&
             transfer != (int)primary->nclx->transfer_characteristics)) return 0;
    } else if (primary->icc_size) {
        /* ICC is the authoritative RGB color description, as in the raster
         * pipeline. Camera HEVC can declare an SDR transfer (e.g. BT.709)
         * alongside a primary ICC without a container NCLX. Do not guess an
         * ICC/CICP correlation; preserve the ICC for the validated transform.
         * PQ/HLG remain rejected above, and all tile ICCs must match. */
        return 1;
    }
    return primary->icc_size ||
        ((primaries == heif_color_primaries_ITU_R_BT_709_5 ||
          primaries == heif_color_primaries_unspecified) &&
         (transfer == heif_transfer_characteristic_IEC_61966_2_1 ||
          transfer == heif_transfer_characteristic_unspecified));
}

static int decoded_layout(const heif_image *image, int native) {
    if (!native) {
        return heif_image_get_colorspace(image) == heif_colorspace_RGB &&
            heif_image_get_chroma_format(image) == heif_chroma_interleaved_RGBA &&
            heif_image_get_bits_per_pixel_range(image, heif_channel_interleaved) == 8;
    }
    heif_channel channels[3];
    switch (heif_image_get_colorspace(image)) {
    case heif_colorspace_YCbCr:
        channels[0] = heif_channel_Y;
        channels[1] = heif_channel_Cb;
        channels[2] = heif_channel_Cr;
        break;
    case heif_colorspace_RGB:
        channels[0] = heif_channel_R;
        channels[1] = heif_channel_G;
        channels[2] = heif_channel_B;
        break;
    default:
        return 0;
    }
    if (heif_image_has_channel(image, heif_channel_Alpha)) return 0;
    for (int i = 0; i < 3; ++i) {
        if (heif_image_get_bits_per_pixel_range(image, channels[i]) != 8) return 0;
    }
    return 1;
}

static int decode_checked(const heif_image_handle *handle, const image_profile *profile,
                           const image_profile *primary, const heif_decoding_options *options,
                           uint32_t max_pixels, int native, heif_image **image) {
    /* RGB conversion can drop NCLX metadata. The native prepass checks the
     * decoder-resolved native color and actual depth; the final RGB pass
     * checks output shape and preservation of the selected ICC. Container
     * color declarations take precedence over bitstream metadata in libheif. */
    heif_error error = heif_decode_image(handle, image,
                       native ? heif_colorspace_undefined : heif_colorspace_RGB,
                       native ? heif_chroma_undefined : heif_chroma_interleaved_RGBA, options);
    if (error.code != heif_error_Ok || !*image ||
        !decoded_layout(*image, native) ||
        heif_image_is_premultiplied_alpha(*image) ||
        heif_image_get_decoding_warnings(*image, 0, NULL, 0) != 0 ||
        !dimensions(heif_image_get_primary_width(*image),
                    heif_image_get_primary_height(*image), max_pixels) ||
        (native && !decoded_color(*image, primary))) return 0;

    size_t output_size = heif_image_get_raw_color_profile_size(*image);
    if (output_size != profile->icc_size) return 0;
    if (!output_size) return 1;
    uint8_t *output = malloc(output_size);
    if (!output) return 0;
    error = heif_image_get_raw_color_profile(*image, output);
    int ok = error.code == heif_error_Ok && memcmp(profile->icc, output, output_size) == 0;
    free(output);
    return ok;
}

/* Coded leaf items must not refer to more image data or request premultiplied
 * interpretation. Container parsing and reference bounds belong to libheif. */
static int leaf_references(heif_context *context, heif_item_id id) {
    for (int i = 0; i <= MAX_TILES; ++i) {
        uint32_t type = 0;
        size_t count = heif_context_get_item_references(context, id, i, &type, NULL);
        if (!count) return 1;
        if (i == MAX_TILES || count > MAX_TILES ||
            type == heif_fourcc('d', 'i', 'm', 'g') ||
            type == heif_fourcc('p', 'r', 'e', 'm')) return 0;
    }
    return 0;
}

/* Grid decoding does not propagate every tile's warnings. Decode each unique
 * coded dependency directly first so mismatched container/bitstream color
 * declarations cannot disappear when the grid copies its tile pixels. */
static int validate_tiles(heif_context *context, const heif_image_handle *primary,
                           const image_profile *primary_profile,
                           const heif_decoding_options *options,
                           uint32_t max_pixels, uint32_t max_profile) {
    heif_image_tiling tiling;
    memset(&tiling, 0, sizeof(tiling));
    if (heif_image_handle_get_image_tiling(primary, 0, &tiling).code != heif_error_Ok ||
        !tiling.num_columns || !tiling.num_rows ||
        (uint64_t)tiling.num_columns * tiling.num_rows > MAX_TILES) return 0;
    heif_item_id seen[MAX_TILES];
    size_t seen_count = 0;
    for (uint32_t y = 0; y < tiling.num_rows; ++y) {
        for (uint32_t x = 0; x < tiling.num_columns; ++x) {
            heif_item_id id = 0;
            if (heif_image_handle_get_grid_image_tile_id(primary, 0, x, y, &id).code != heif_error_Ok ||
                heif_item_get_item_type(context, id) != heif_item_type_hvc1 ||
                !leaf_references(context, id)) return 0;
            size_t i = 0;
            for (; i < seen_count && seen[i] != id; ++i) {}
            if (i < seen_count) continue;
            seen[seen_count++] = id;
            heif_image_handle *handle = NULL;
            heif_image *image = NULL;
            image_profile profile = {0};
            int ok = heif_context_get_image_handle(context, id, &handle).code == heif_error_Ok && handle &&
                read_profile(handle, max_pixels, max_profile, &profile) &&
                compatible_profile(primary_profile, &profile) &&
                decode_checked(handle, &profile, primary_profile, options, max_pixels, 1, &image);
            if (image) heif_image_release(image);
            if (handle) heif_image_handle_release(handle);
            free_profile(&profile);
            if (!ok) return 0;
        }
    }
    return 1;
}

static int write_png(const char *path, int width, int height,
                     const uint8_t *pixels, size_t stride,
                     const uint8_t *profile, size_t profile_size) {
    FILE *file = fopen(path, "wbx");
    if (!file) return 0;
    png_structp png = png_create_write_struct(PNG_LIBPNG_VER_STRING, NULL, NULL, NULL);
    if (!png) { fclose(file); remove(path); return 0; }
    png_infop info = png_create_info_struct(png);
    if (!info) {
        png_destroy_write_struct(&png, NULL);
        fclose(file);
        remove(path);
        return 0;
    }
    if (setjmp(png_jmpbuf(png))) {
        png_destroy_write_struct(&png, &info);
        fclose(file);
        remove(path);
        return 0;
    }
    png_init_io(png, file);
    png_set_compression_level(png, 1);
#ifdef PNG_BENIGN_ERRORS_SUPPORTED
    png_set_benign_errors(png, 0);
#endif
    png_set_IHDR(png, info, (png_uint_32)width, (png_uint_32)height, 8,
                 PNG_COLOR_TYPE_RGBA, PNG_INTERLACE_NONE,
                 PNG_COMPRESSION_TYPE_DEFAULT, PNG_FILTER_TYPE_DEFAULT);
    if (profile_size) {
        png_set_iCCP(png, info, "ICC", PNG_COMPRESSION_TYPE_BASE,
                     profile, (png_uint_32)profile_size);
        png_charp name = NULL;
        png_bytep retained = NULL;
        png_uint_32 retained_size = 0;
        int compression = 0;
        if (!png_get_iCCP(png, info, &name, &compression, &retained, &retained_size) ||
            retained_size != profile_size || !retained ||
            memcmp(retained, profile, profile_size) != 0) {
            png_error(png, "ICC profile was not preserved");
        }
    }
    png_write_info(png, info);
    for (int row = 0; row < height; ++row) {
        png_write_row(png, pixels + (size_t)row * stride);
    }
    png_write_end(png, info);
    png_destroy_write_struct(&png, &info);
    if (fclose(file) != 0) { remove(path); return 0; }
    return 1;
}

int main(int argc, char **argv) {
    uint32_t max_pixels, max_profile;
    if (argc != 5 || !number(argv[3], &max_pixels) || !number(argv[4], &max_profile)) return 2;

    int result = 1;
    heif_context *context = NULL;
    heif_image_handle *handle = NULL;
    heif_image *image = NULL;
    heif_decoding_options *options = NULL;
    image_profile profile = {0};
    heif_error error = heif_init(NULL);
    if (error.code != heif_error_Ok) return 1;

    context = heif_context_alloc();
    if (!context) goto done;
    heif_context_set_max_decoding_threads(context, 1);
    error = heif_context_read_from_file(context, argv[1], NULL);
    if (error.code != heif_error_Ok) goto done;

    heif_item_id primary = 0;
    error = heif_context_get_primary_image_ID(context, &primary);
    if (error.code != heif_error_Ok) goto done;
    uint32_t primary_type = heif_item_get_item_type(context, primary);
    if (primary_type != heif_item_type_grid && primary_type != heif_item_type_hvc1) goto done;
    error = heif_context_get_image_handle(context, primary, &handle);
    if (error.code != heif_error_Ok || !handle ||
        !heif_image_handle_is_primary_image(handle) ||
        heif_image_handle_get_item_id(handle) != primary ||
        !read_profile(handle, max_pixels, max_profile, &profile)) goto done;

    options = heif_decoding_options_alloc();
    if (!options || options->version < 10) goto done;
    options->ignore_transformations = 0;
    options->convert_hdr_to_8bit = 0;
    options->strict_decoding = 1;
    options->num_codec_threads = 1;
    options->num_library_threads = 1;
    options->autocorrect_broken_input = 0;
    /* Without this flag a NULL target NCLX requests an implicit sRGB
     * conversion, which would make reusing the source ICC incorrect. */
    options->output_image_nclx_profile = NULL;
    options->output_image_nclx_profile_passthrough = 1;
    if (primary_type == heif_item_type_grid) {
        if (!validate_tiles(context, handle, &profile, options, max_pixels, max_profile)) goto done;
    } else {
        if (!leaf_references(context, primary) ||
            !decode_checked(handle, &profile, &profile, options, max_pixels, 1, &image)) goto done;
        heif_image_release(image);
        image = NULL;
    }
    if (!decode_checked(handle, &profile, &profile, options, max_pixels, 0, &image)) goto done;

    int width = heif_image_get_width(image, heif_channel_interleaved);
    int height = heif_image_get_height(image, heif_channel_interleaved);
    if (!dimensions(width, height, max_pixels)) goto done;
    size_t stride = 0;
    const uint8_t *pixels = heif_image_get_plane_readonly2(image,
                                        heif_channel_interleaved, &stride);
    if (!pixels || (uint64_t)width * 4 > SIZE_MAX ||
        stride < (size_t)width * 4 || stride > SIZE_MAX / (size_t)height) goto done;

    result = write_png(argv[2], width, height, pixels, stride,
                       profile.icc, profile.icc_size) ? 0 : 1;

done:
    free_profile(&profile);
    if (image) heif_image_release(image);
    if (options) heif_decoding_options_free(options);
    if (handle) heif_image_handle_release(handle);
    if (context) heif_context_free(context);
    heif_deinit();
    return result;
}
