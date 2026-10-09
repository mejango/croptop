import java.net.URI
import java.io.File
import org.gradle.api.tasks.bundling.AbstractArchiveTask

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// This value owns both the compiled client default and the manifest association.
val mobileOrigin = providers.gradleProperty("croptop.mobileOrigin").get()
val mobileURI = URI(mobileOrigin)
require(mobileURI.scheme == "https" && !mobileURI.host.isNullOrBlank() && mobileURI.rawAuthority == mobileURI.host && mobileURI.rawPath.isNullOrEmpty() && mobileURI.rawQuery == null && mobileURI.rawFragment == null) {
    "croptop.mobileOrigin must be an exact HTTPS origin without credentials, port, path, query or fragment."
}
val signingVariables = listOf("CROPTOP_ANDROID_KEYSTORE", "CROPTOP_ANDROID_STORE_PASSWORD", "CROPTOP_ANDROID_KEY_ALIAS", "CROPTOP_ANDROID_KEY_PASSWORD")
val releaseInputs = signingVariables.associateWith { providers.environmentVariable(it).orNull }
val hasSigningInput = releaseInputs.values.any { it != null }
val signingRequirement = providers.gradleProperty("croptop.requireReleaseSigning").orNull
require(signingRequirement == null || signingRequirement in listOf("true", "false")) { "croptop.requireReleaseSigning must be exactly true or false." }
val requireSigning = signingRequirement == "true"
require(!hasSigningInput || releaseInputs.values.all { !it.isNullOrBlank() }) {
    "Incomplete Android signing configuration. Set all four CROPTOP_ANDROID signing variables or unset all of them for an unsigned build."
}
require(!requireSigning || hasSigningInput) { "Release signing was required, but no external keystore was supplied. No debug signing fallback is permitted." }
val externalKeystore = if (hasSigningInput) File(releaseInputs.getValue("CROPTOP_ANDROID_KEYSTORE")!!) else null
require(externalKeystore == null || (externalKeystore.isAbsolute && externalKeystore.isFile && externalKeystore.canRead())) { "The external Android release keystore must be an existing readable absolute file path." }

android {
    namespace = "top.crop.mobile"
    compileSdk = 36
    buildFeatures { buildConfig = true }
    defaultConfig {
        applicationId = "top.crop.mobile"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        testInstrumentationRunner = "top.crop.mobile.NativeInstrumentationRunner"
        buildConfigField("String", "MOBILE_SERVICE_ORIGIN", "\"$mobileOrigin\"")
        manifestPlaceholders["mobileServiceHost"] = mobileURI.host
    }
    signingConfigs {
        if (hasSigningInput) create("externalRelease") {
            storeFile = externalKeystore
            storePassword = releaseInputs.getValue("CROPTOP_ANDROID_STORE_PASSWORD")
            keyAlias = releaseInputs.getValue("CROPTOP_ANDROID_KEY_ALIAS")
            keyPassword = releaseInputs.getValue("CROPTOP_ANDROID_KEY_PASSWORD")
        }
    }
    buildTypes {
        release {
            isDebuggable = false
            signingConfig = if (hasSigningInput) signingConfigs.getByName("externalRelease") else null
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    sourceSets.getByName("test").resources.srcDir("../../../docs/design")
    sourceSets.getByName("test").resources.srcDir("../../../testdata")
}
tasks.withType<AbstractArchiveTask>().configureEach {
    isPreserveFileTimestamps = false
    isReproducibleFileOrder = true
}
dependencies {
    implementation("org.bouncycastle:bcprov-jdk18on:1.80")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20250107")
    androidTestCompileOnly(files(
        android.sdkDirectory.resolve("platforms/android-${android.compileSdk}/optional/android.test.base.jar"),
        android.sdkDirectory.resolve("platforms/android-${android.compileSdk}/optional/android.test.runner.jar"),
    ))
}
