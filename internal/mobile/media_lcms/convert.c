/* Croptop's narrow ICC adapter. Input is a private PNG re-encoded by Go, not
 * an uploaded file. lcms2 (MIT) and libpng (libpng-2.0) are dynamically linked.
 * No format discovery, profile fallback, network, or metadata passthrough. */
#include <lcms2.h>
#include <png.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char **argv) {
    if (argc != 6) return 2;
    char *end = NULL;
    unsigned long max_pixels = strtoul(argv[4], &end, 10);
    if (!end || *end || !max_pixels || max_pixels > UINT32_MAX) return 2;
    unsigned long max_profile = strtoul(argv[5], &end, 10);
    if (!end || *end || !max_profile || max_profile > UINT32_MAX) return 2;
    FILE *file = fopen(argv[2], "rb");
    if (!file) return 1;
    if (fseek(file, 0, SEEK_END) != 0) { fclose(file); return 1; }
    long length = ftell(file);
    if (length <= 0 || (unsigned long)length > max_profile || fseek(file, 0, SEEK_SET) != 0) { fclose(file); return 1; }
    void *profile_bytes = malloc((size_t)length);
    if (!profile_bytes) { fclose(file); return 1; }
    if (fread(profile_bytes, 1, (size_t)length, file) != (size_t)length) { free(profile_bytes); fclose(file); return 1; }
    fclose(file);
    cmsHPROFILE input = cmsOpenProfileFromMem(profile_bytes, (cmsUInt32Number)length);
    free(profile_bytes);
    if (!input) return 1;
    if (cmsGetColorSpace(input) != cmsSigRgbData ||
        (cmsGetDeviceClass(input) != cmsSigDisplayClass && cmsGetDeviceClass(input) != cmsSigInputClass)) {
        cmsCloseProfile(input); return 1;
    }
    cmsHPROFILE output = cmsCreate_sRGBProfile();
    if (!output) { cmsCloseProfile(input); return 1; }
    cmsHTRANSFORM transform = cmsCreateTransform(input, TYPE_RGBA_8, output, TYPE_RGBA_8,
        INTENT_RELATIVE_COLORIMETRIC, cmsFLAGS_COPY_ALPHA | cmsFLAGS_BLACKPOINTCOMPENSATION);
    cmsCloseProfile(input);
    cmsCloseProfile(output);
    if (!transform) return 1;
    png_image image;
    memset(&image, 0, sizeof(image));
    image.version = PNG_IMAGE_VERSION;
    if (!png_image_begin_read_from_file(&image, argv[1])) { cmsDeleteTransform(transform); return 1; }
    if (!image.width || !image.height || (uint64_t)image.width * image.height > max_pixels) {
        png_image_free(&image); cmsDeleteTransform(transform); return 1;
    }
    image.format = PNG_FORMAT_RGBA;
    void *pixels = malloc(PNG_IMAGE_SIZE(image));
    if (!pixels) { png_image_free(&image); cmsDeleteTransform(transform); return 1; }
    if (!png_image_finish_read(&image, NULL, pixels, 0, NULL)) {
        free(pixels); png_image_free(&image); cmsDeleteTransform(transform); return 1;
    }
    cmsDoTransform(transform, pixels, pixels, image.width * image.height);
    cmsDeleteTransform(transform);
    int ok = png_image_write_to_file(&image, argv[3], 0, pixels, 0, NULL);
    free(pixels);
    png_image_free(&image);
    return ok ? 0 : 1;
}
