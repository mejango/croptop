package top.crop.mobile

/** A dismissed confirmation must never strand a live capability with no UI. */
internal class PairingTasks(
    private val background: (String, () -> Unit) -> Boolean,
    private val cancel: () -> Unit,
) {
    fun submit(message: String, task: () -> Unit): Boolean {
        val accepted = background(message) {
            try { task() } catch (error: Exception) {
                cancel()
                throw error
            }
        }
        if (!accepted) cancel()
        return accepted
    }
}
