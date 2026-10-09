package top.crop.mobile

import android.graphics.Bitmap
import android.graphics.BitmapFactory

/** Bounds are checked before decoding; a displayable preview is required before signing. */
object ImagePreview {
    fun decode(bytes: ByteArray, maxPixels: Long): Bitmap {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
        require(bounds.outWidth > 0 && bounds.outHeight > 0 && bounds.outWidth.toLong() * bounds.outHeight <= maxPixels) { "This preview cannot be displayed safely. Your draft is still saved." }
        var sample = 1
        while (maxOf(bounds.outWidth, bounds.outHeight) / sample > 1600) sample *= 2
        val options = BitmapFactory.Options().apply { inSampleSize = sample }
        return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options) ?: error("The prepared preview could not be decoded. Do not publish it; check status to try again.")
    }
    fun validate(bytes: ByteArray, maxPixels: Long) { decode(bytes, maxPixels).recycle() }
}
