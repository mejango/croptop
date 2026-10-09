package top.crop.mobile

import org.junit.Assert.*
import org.junit.Test

class PairingTasksTest {
    @Test fun failedClaimReleasesFlowForFreshLink() = terminalFailure("claim")
    @Test fun failedConsumeReleasesFlowForFreshLink() = terminalFailure("consume")

    private fun terminalFailure(stage: String) {
        var active = true
        var pending: (() -> Unit)? = null
        val tasks = PairingTasks({ _, task -> pending = task; true }, { active = false })
        assertTrue(tasks.submit(stage) { error("expired") })
        assertTrue(active)
        assertThrows(IllegalStateException::class.java) { pending!!() }
        assertFalse("A fresh link must not be blocked by a failed $stage", active)
    }

    @Test fun queuedShareDeclinesSetupAndReleasesFlow() {
        var active = true
        var executed = false
        val tasks = PairingTasks({ _, _ -> false }, { active = false })
        assertFalse(tasks.submit("claim") { executed = true })
        assertFalse(active)
        assertFalse(executed)
    }

    @Test fun handledPendingResponseKeepsExplicitRetryFlow() {
        var active = true
        var pending: (() -> Unit)? = null
        val tasks = PairingTasks({ _, task -> pending = task; true }, { active = false })
        assertTrue(tasks.submit("consume") { /* pairing_pending is handled with a retry dialog */ })
        pending!!()
        assertTrue(active)
    }
}
