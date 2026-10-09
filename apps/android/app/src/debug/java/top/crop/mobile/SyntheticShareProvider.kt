package top.crop.mobile

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.OpenableColumns
import java.io.File
import java.io.FileNotFoundException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/** A debug-only URI source which cannot expose drafts, keys, or arbitrary files. */
class SyntheticShareProvider : ContentProvider() {
    override fun onCreate() = true
    private fun fixture(uri: Uri): File {
        if (uri.path != "/screenshot.png") throw FileNotFoundException("Unknown synthetic image")
        return File(requireNotNull(context).cacheDir, "native-test-share.png")
    }
    override fun getType(uri: Uri): String { fixture(uri); return "image/png" }
    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw FileNotFoundException("Read only")
        openCount.incrementAndGet()
        openStarted?.countDown()
        if (openGate?.await(12, TimeUnit.SECONDS) == false) throw FileNotFoundException("Synthetic test stream timed out")
        return ParcelFileDescriptor.open(fixture(uri), ParcelFileDescriptor.MODE_READ_ONLY)
    }
    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor {
        val file = fixture(uri)
        return MatrixCursor(arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)).apply {
            addRow(arrayOf<Any>("screenshot.png", file.length()))
        }
    }
    override fun insert(uri: Uri, values: ContentValues?): Uri? = throw UnsupportedOperationException("Read only")
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException("Read only")
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = throw UnsupportedOperationException("Read only")

    companion object {
        val openCount = AtomicInteger()
        @Volatile var openStarted: CountDownLatch? = null
        @Volatile var openGate: CountDownLatch? = null
    }
}
