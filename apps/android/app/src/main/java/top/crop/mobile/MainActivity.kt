package top.crop.mobile

import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.graphics.BitmapFactory
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.MediaStore
import android.text.Editable
import android.text.InputType
import android.text.TextWatcher
import android.view.View
import android.view.WindowManager
import android.view.WindowInsets
import android.widget.Button
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

private object Work {
    val executor = Executors.newSingleThreadExecutor()
    val pending = AtomicInteger(0)
}

/** Native photo intake + review composer. No WebView or private key transport. */
class MainActivity : Activity() {
    private lateinit var store: DraftStore
    private lateinit var keys: SiteKeyStore
    private lateinit var api: MobileApi
    private lateinit var publisher: Publisher
    private lateinit var content: LinearLayout
    private val handler = Handler(Looper.getMainLooper())
    private val workRefresh = Runnable { if (!destroyed) render() }
    private var selected: String? = null
    private var status = ""
    private var destroyed = false
    @Volatile private var resumed = false
    private var showingSetup = false
    private var site: JSONObject? = null
    private var config: JSONObject? = null
    private var caption: EditText? = null
    private var title: EditText? = null
    private var keyInput: EditText? = null
    private var pairing: PairingFlow? = null
    private var pairingInputDialog: AlertDialog? = null
    private val preferences by lazy { getSharedPreferences("composer", MODE_PRIVATE) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        store = DraftStore(File(noBackupFilesDir, "drafts"))
        keys = SiteKeyStore(this)
        api = MobileApi(keys)
        publisher = Publisher(keys, store, api)
        selected = savedInstanceState?.getString("draft") ?: preferences.getString("draft", null)
        window.decorView.setOnApplyWindowInsetsListener { view, insets ->
            if (android.os.Build.VERSION.SDK_INT >= 30) {
                val safe = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout() or WindowInsets.Type.ime())
                view.setPadding(safe.left, safe.top, safe.right, safe.bottom)
            } else {
                @Suppress("DEPRECATION") view.setPadding(insets.systemWindowInsetLeft, insets.systemWindowInsetTop, insets.systemWindowInsetRight, insets.systemWindowInsetBottom)
            }
            insets
        }
        render()
        if (savedInstanceState == null) receive(intent) else discardIncomingIntent(intent)
    }

    override fun onResume() { super.onResume(); resumed = true }
    override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); setIntent(intent); receive(intent) }
    override fun onSaveInstanceState(outState: Bundle) { persistText(); outState.putString("draft", selected); super.onSaveInstanceState(outState) }
    override fun onPause() { resumed = false; persistText(); keyInput?.text?.clear(); pairingInputDialog?.dismiss(); pairingInputDialog = null; pairing?.cancel(); pairing = null; super.onPause() }
    override fun onDestroy() { destroyed = true; pairing?.cancel(); pairing = null; handler.removeCallbacksAndMessages(null); super.onDestroy() }

    private fun current(): Draft? = selected?.let { runCatching { store.get(it) }.getOrNull() }
    private fun connection(): Connection? = runCatching { keys.connection() }.getOrElse {
        status = "Your site connection could not be read. Import your key again; saved drafts remain available."
        null
    }
    private fun choose(draft: Draft) {
        selected = draft.id
        require(preferences.edit().putString("draft", selected).commit()) { "Could not retain the selected draft." }
    }
    private fun persistText() {
        val draft = current() ?: return
        if (!draft.submitted && caption != null && title != null) {
            runCatching { store.save(draft.copy(caption = caption!!.text.toString(), title = title!!.text.toString())) }
                .onFailure { status = "Could not save your text. Check available storage before closing Croptop." }
        }
    }

    private fun receive(incoming: Intent) {
        // Retain only the values needed for intake. Capability-bearing URLs and
        // provider URIs must not remain in Activity state across recreation.
        val transient = Intent(incoming)
        discardIncomingIntent(incoming)
        try { receiveIntent(transient) }
        catch (error: Exception) { status = error.message ?: "This shared item could not be opened. Your saved drafts are retained."; render() }
        finally { transient.data = null; transient.clipData = null; transient.replaceExtras(null as Bundle?) }
    }

    private fun discardIncomingIntent(incoming: Intent) {
        incoming.data = null
        incoming.clipData = null
        incoming.replaceExtras(null as Bundle?)
        incoming.action = Intent.ACTION_MAIN
        setIntent(Intent(this, MainActivity::class.java).setAction(Intent.ACTION_MAIN))
    }

    private fun receiveIntent(incoming: Intent) {
        if (incoming.action == Intent.ACTION_SEND_MULTIPLE || (incoming.clipData?.itemCount ?: 0) > 1) {
            status = "Croptop posts one image at a time. Share a single screenshot."
            render(); return
        }
        if (incoming.action == Intent.ACTION_SEND) {
            @Suppress("DEPRECATION") val uri = incoming.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)
                ?: incoming.clipData?.getItemAt(0)?.uri
            if (uri == null) { status = "Share one screenshot from Photos or Files."; render(); return }
            intake(uri, incoming.type)
        } else if (incoming.action == Intent.ACTION_VIEW) {
            incoming.data?.let { openPairing(it.toString()) }
        }
    }

    private fun intake(uri: Uri, mimeHint: String?) {
        persistText()
        runBackground("Saving screenshot…", allowQueue = true) {
            require(uri.scheme == "content") { "Choose an image shared by Photos or Files." }
            val mime = contentResolver.getType(uri) ?: mimeHint ?: ""
            val maxBytes = config?.optLong("maxImageBytes", 20L * 1024 * 1024) ?: 20L * 1024 * 1024
            val draft = contentResolver.openInputStream(uri)?.use { store.intake(it, mime, maxBytes) }
                ?: error("The image is unavailable. Download it in Photos and share it again.")
            choose(draft)
            status = if (connection() == null) "Screenshot saved. Connect your site to publish it." else "Screenshot saved on this phone."
        }
    }

    private fun picker() {
        persistText()
        val picker = if (android.os.Build.VERSION.SDK_INT >= 33) Intent(MediaStore.ACTION_PICK_IMAGES).setType("image/*")
            else Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("image/*")
        startActivityForResult(picker, 100)
    }

    @Deprecated("Framework Activity compatibility")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (resultCode != RESULT_OK || data?.data == null) return
        if (requestCode == 100) intake(data.data!!, data.type)
        if (requestCode == 101) {
            try {
                val pem = contentResolver.openInputStream(data.data!!)?.use { String(MobileApi.readBounded(it, 8192), Charsets.UTF_8) }
                    ?: error("The key file could not be opened.")
                keyInput?.setText(pem)
            } catch (error: Exception) { status = error.message ?: "The key file could not be read."; render() }
        }
    }

    private fun background(message: String, task: () -> Unit): Boolean = runBackground(message, false, task)

    private fun runBackground(message: String, allowQueue: Boolean, task: () -> Unit): Boolean {
        if (allowQueue) Work.pending.incrementAndGet()
        else if (!Work.pending.compareAndSet(0, 1)) return false
        persistText()
        status = message
        render()
        Work.executor.execute {
            try { task() } catch (error: Exception) {
                status = error.message ?: "The connection was interrupted. Your draft is saved; retry to check its status."
                current()?.let { draft -> runCatching { store.save(draft.copy(error = status)) } }
            } finally {
                Work.pending.decrementAndGet()
                handler.post { if (!destroyed) render() }
            }
        }
        return true
    }

    private fun render() {
        // An old progress refresh must not rebuild idle fields or dismiss their keyboard.
        handler.removeCallbacks(workRefresh)
        selected = preferences.getString("draft", selected)
        caption = null; title = null
        val scroll = ScrollView(this).apply { isFillViewport = true }
        content = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding(dp(24), dp(16), dp(24), dp(32)) }
        scroll.addView(content)
        setContentView(scroll)
        label("Croptop", 32, bold = true)
        label("A screenshot, a little context.", 16, Color.rgb(92, 99, 87))
        if (status.isNotEmpty()) label(status, 15).apply { accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE }
        if (Work.pending.get() > 0) {
            label("You can reopen Croptop to recover a pending publication.", 14)
            handler.postDelayed(workRefresh, 1000)
            return
        }
        val connection = connection()
        if (showingSetup || connection == null) {
            setup()
            if (connection != null) action("Back to screenshot") { showingSetup = false; render() }
        } else {
            label(site?.optString("name")?.ifBlank { "Connected site" } ?: "Connected site", 17, bold = true)
            label(connection.ipns, 12).apply { setTextIsSelectable(true) }
            action("Site settings") { siteSettings() }
        }
        val draft = current()
        if (draft == null) {
            action("Choose screenshot") { picker() }
            label("Or open a screenshot, tap Share, and choose Croptop.", 15)
        } else {
            composer(draft, connection)
        }
        val drafts = runCatching { store.all() }.getOrElse { label("Saved drafts could not be read. Your files have been retained.", 14); emptyList() }
        if (store.corruptedCount() > 0) label("Some saved drafts could not be read. Their files are still retained on this phone.", 14)
        if (drafts.isNotEmpty()) {
            label("On this phone", 20, bold = true)
            drafts.forEach { saved ->
                action("${saved.title.ifBlank { "Screenshot" }} · ${stateLabel(saved.state)}") { persistText(); choose(saved); status = ""; showingSetup = false; render() }
            }
        }
        action("Privacy and storage") {
            AlertDialog.Builder(this).setTitle("Privacy and storage")
                .setMessage(getString(R.string.privacy_and_storage)).setPositiveButton("Done", null).show()
        }
    }

    private fun setup() {
        label("Connect your site", 23, bold = true)
        label("Connect from your existing Croptop publisher or import its site key. Your screenshot stays saved during setup.", 15)
        action("Open connection link") {
            val input = EditText(this).apply {
                hint = "Paste the Connect phone link"
                inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
                imeOptions = android.view.inputmethod.EditorInfo.IME_FLAG_NO_PERSONALIZED_LEARNING
                importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
                isSaveEnabled = false
            }
            pairingInputDialog = AlertDialog.Builder(this).setTitle("Connect phone").setView(input)
                .setPositiveButton("Continue") { _, _ -> val link = input.text.toString(); input.text.clear(); openPairing(link) }
                .setNegativeButton("Cancel", null).create().apply {
                    setOnDismissListener { input.text.clear(); if (pairingInputDialog === this) pairingInputDialog = null }
                    show()
                }
        }
        keyInput = input("Site key (PKCS8 PEM)", "", true).apply {
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_VARIATION_PASSWORD
            minLines = 3
            importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
            imeOptions = android.view.inputmethod.EditorInfo.IME_FLAG_NO_PERSONALIZED_LEARNING
            isSaveEnabled = false
        }
        action("Choose key file") { startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), 101) }
        label("The site key grants full control of your site. Croptop encrypts it on this phone. Keep your original key backup.", 14)
        action("Import site key") {
            val pem = keyInput!!.text.toString()
            val origin = PublishingService.origin
            val existing = keys.snapshot()
            if (existing.connection != null) {
                AlertDialog.Builder(this).setTitle("Replace this phone’s connection?").setMessage("Existing drafts keep their original destination. Keep a backup of your original site's key.")
                    .setPositiveButton("Replace") { _, _ -> importSite(pem, origin, existing) }.setNegativeButton("Cancel", null).show()
            } else importSite(pem, origin, existing)
        }
    }

    private fun importSite(pem: String, origin: String, expected: ConnectionSnapshot) {
        keyInput?.text?.clear()
        background("Checking your site…") {
            require(resumed && !destroyed && !isFinishing) { "Connection setup was closed. Your draft is still saved." }
            keys.import(pem, origin, expected)
            site = null
            config = null
            showingSetup = false
            config = api.config()
            site = api.site()
            status = site!!.optString("reason").ifBlank { "Site connected. Enable phone publishing in Site settings to continue." }
        }
    }

    private fun siteSettings() {
        val expected = keys.snapshot()
        val actions = arrayOf("Check site readiness", "Enable phone publishing", "Stop phone publishing", "Connect another site", "Remove site from this phone")
        AlertDialog.Builder(this).setTitle("Site settings").setItems(actions) { _, index ->
            when (index) {
                0 -> background("Checking your site…") { config = api.config(); site = api.site(); status = site!!.optString("reason").ifBlank { if (site!!.getBoolean("ready")) "Your site is ready." else "Publish a compatible hosted version from your existing Croptop publisher." } }
                1 -> AlertDialog.Builder(this).setTitle("Enable phone publishing?").setMessage("This hosted service prepares your posts. Your key stays on this phone and signs each publication after you review it.")
                    .setPositiveButton("Enable") { _, _ -> background("Enabling phone publishing…") { keys.requireCurrent(expected); config = api.config(); site = api.consent(true); status = "Phone publishing enabled." } }.setNegativeButton("Cancel", null).show()
                2 -> background("Stopping phone publishing…") { keys.requireCurrent(expected); site = api.consent(false); status = "New publications are stopped. Already published posts remain online." }
                3 -> { showingSetup = true; render() }
                4 -> AlertDialog.Builder(this).setTitle("Remove local connection?").setMessage("This deletes the key from this phone. Drafts stay saved. Other copies of the site key remain valid.")
                    .setPositiveButton("Remove") { _, _ -> background("Removing the local connection…") { keys.remove(expected); site = null; config = null; status = "Local connection removed. Saved drafts remain on this phone." } }.setNegativeButton("Cancel", null).show()
            }
        }.show()
    }

    private fun composer(draft: Draft, connection: Connection?) {
        label(if (draft.state == "published") "Published" else "Your screenshot", 23, bold = true)
        val preview = if (draft.state == "needs_signature" && store.preview(draft.id).exists()) store.preview(draft.id) else store.source(draft.id)
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(preview.path, bounds)
        val options = BitmapFactory.Options().apply { inSampleSize = maxOf(1, maxOf(bounds.outWidth, bounds.outHeight) / 1200) }
        val bitmap = runCatching { BitmapFactory.decodeFile(preview.path, options) }.getOrNull()
        if (bitmap != null) content.addView(ImageView(this).apply { setImageBitmap(bitmap); adjustViewBounds = true; maxHeight = dp(320); contentDescription = "Screenshot preview" }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(16) })
        else label("Image saved. Its prepared preview will appear before publication.", 14)
        if (draft.ipns.isNotEmpty()) label("Destination: ${draft.ipns}", 12)
        else if (connection != null) label("Publish to: ${connection.ipns}", 12)
        caption = input("Add a caption (optional)", draft.caption, true).apply { isEnabled = !draft.submitted }
        title = input("Title (optional)", draft.title, false).apply { isEnabled = !draft.submitted }
        val watcher = object : TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) { persistText() }
            override fun afterTextChanged(s: Editable?) {}
        }
        caption!!.addTextChangedListener(watcher); title!!.addTextChangedListener(watcher)
        if (draft.state == "published") {
            label("Your screenshot is online.", 16)
            action("Open post") { openPublished(draft.publishedURL) }
            action("Copy post link") { (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("Croptop post", draft.publishedURL)); status = "Post link copied."; render() }
        } else if (connection != null) {
            if (draft.state == "failed" && JSONObject(draft.operation).optString("code") == "draft_expired") {
                label("The service confirmed this upload expired before publication. Your original screenshot is still saved.", 15)
                action("Create new draft from screenshot") {
                    AlertDialog.Builder(this).setTitle("Start a new draft?").setMessage("The expired operation stays in your history. A new draft will use the saved screenshot and the same destination.")
                        .setPositiveButton("Create draft") { _, _ -> background("Retaining your screenshot…") { choose(store.renewExpired(store.get(draft.id))); status = "New draft saved. Review it before publishing." } }
                        .setNegativeButton("Cancel", null).show()
                }
            } else if (draft.state == "failed" && !draft.authorized && JSONObject(draft.operation).optJSONObject("proposal") == null) {
                label(JSONObject(draft.operation).optString("error", "This screenshot could not be prepared."), 15)
                action("Edit this draft") {
                    AlertDialog.Builder(this).setTitle("Create an editable copy?").setMessage("Croptop will recheck that this unsigned preparation was rejected. The original operation stays in your history.")
                        .setPositiveButton("Edit draft") { _, _ -> background("Checking the rejected preparation…") { choose(publisher.reviseRejected(store.get(draft.id))); status = "Editable draft saved. You can revise the text or choose another image." } }
                        .setNegativeButton("Cancel", null).show()
                }
            }
            val sameDestination = draft.ipns.isEmpty() || (draft.ipns == connection.ipns && draft.origin == connection.origin)
            if (!sameDestination) label("Reconnect the original site to publish this draft.", 15)
            else {
                if (draft.state == "needs_signature" && store.preview(draft.id).exists() && bitmap != null) action("Publish screenshot") {
                    background("Publishing…") {
                        val result = publisher.publish(store.get(draft.id))
                        status = if (result.state == "published") "Published. Your post link is ready." else "Publication is pending. Check status to confirm it."
                    }
                }
                action(if (draft.submitted) "Check status / retry" else "Prepare preview") {
                    persistText()
                    var retained = store.get(draft.id)
                    if (retained.ipns.isEmpty()) { retained = retained.copy(ipns = connection.ipns, origin = connection.origin); store.save(retained) }
                    val destination = retained
                    background("Preparing your screenshot…") {
                        val result = publisher.review(destination)
                        status = when (result.state) {
                            "published" -> "Published. Your post link is ready."
                            "needs_signature" -> "Review the prepared screenshot, then Publish."
                            "failed" -> JSONObject(result.operation).optString("error", "Preparation failed. Retry with the same saved draft.")
                            else -> "${stateLabel(result.state)}. Check status to continue."
                        }
                    }
                }
            }
        }
        if (!draft.submitted) action("Choose another screenshot") { picker() }
        else action("New screenshot") { picker() }
        action("Save draft and close") { persistText(); finish() }
    }

    private fun openPublished(value: String) {
        val uri = Uri.parse(value)
        require(uri.scheme == "https" && uri.host != null) { "The service returned an invalid post link." }
        startActivity(Intent(Intent.ACTION_VIEW, uri))
    }

    private fun openPairing(link: String) {
        // Pairing is handled by the same authenticated encryption protocol as the website.
        try {
            val parsed = PairingLink.parse(link, PublishingService.origin)
            require(pairing?.isActive() != true) { "Connection setup is already open. Finish or cancel it before opening another link." }
            require(Work.pending.get() == 0) { "Finish the current operation, then open the connection link again. Your draft remains saved." }
            pairing = PairingFlow(this, keys, { message -> status = message; site = null; config = null; showingSetup = false; render() }, ::background)
            pairing!!.open(parsed)
        }
        catch (error: Exception) { status = error.message ?: "Create a fresh Connect phone link on your existing publisher."; render() }
    }

    private fun stateLabel(state: String) = when (state) {
        "draft" -> "Saved draft"; "unconfirmed" -> "Awaiting confirmation"; "preparing" -> "Preparing";
        "needs_signature" -> "Ready to publish"; "committing" -> "Publication pending"; "published" -> "Published"; else -> "Needs attention"
    }
    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()
    private fun label(value: String, size: Int, color: Int = Color.rgb(35, 43, 30), bold: Boolean = false): TextView {
        val view = TextView(this).apply { text = value; textSize = size.toFloat(); setTextColor(color); if (bold) setTypeface(typeface, Typeface.BOLD) }
        content.addView(view, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(12); bottomMargin = dp(4) })
        return view
    }
    private fun input(hint: String, value: String, multiline: Boolean): EditText {
        val view = EditText(this).apply {
            this.hint = hint; setText(value); textSize = 16f
            inputType = InputType.TYPE_CLASS_TEXT or if (multiline) InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_FLAG_CAP_SENTENCES else InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
            minLines = if (multiline) 2 else 1
            setSingleLine(!multiline)
            setPadding(dp(8), dp(12), dp(8), dp(12))
        }
        content.addView(view, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(12) })
        return view
    }
    private fun action(text: String, action: () -> Unit) {
        content.addView(Button(this).apply { this.text = text; isAllCaps = false; minHeight = dp(48); setOnClickListener { try { action() } catch (error: Exception) { status = error.message ?: "Please try again."; render() } } }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(8) })
    }
}
