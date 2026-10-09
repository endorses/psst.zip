import groovy.json.JsonOutput
import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.ksp)
}

val releaseVersion =
    Properties().apply {
        rootProject.file("release-version.properties").inputStream().use { load(it) }
    }
val androidVersionName = releaseVersion.getProperty("versionName")
val deviceTestBuildType =
    providers.environmentVariable("PSST_ANDROID_DEVICE_TEST_BUILD_TYPE").orNull

require(deviceTestBuildType == null || deviceTestBuildType in setOf("debug", "release")) {
    "Device test build type must be debug or release"
}

val deviceTestVersionCode =
    providers.environmentVariable("PSST_ANDROID_DEVICE_TEST_VERSION_CODE").orNull

require(deviceTestVersionCode == null || deviceTestBuildType != null) {
    "Private device-fixture version overrides require an explicit test build type"
}

// Fixture APKs deliberately disagree with the checked-in release version and
// cannot pass ordinary publication validation. Production jobs set neither input.
val androidVersionCode =
    (deviceTestVersionCode ?: releaseVersion.getProperty("versionCode")).toInt()

require(androidVersionName.matches(Regex("[0-9]+\\.[0-9]+\\.[0-9]+"))) {
    "Android versionName must match android-vX.Y.Z release tags"
}

require(androidVersionCode in 1..2100000000) {
    "Android versionCode is outside the supported range"
}

val noUntrackedMobileSource =
    providers
        .exec {
            workingDir(rootProject.projectDir.parentFile)
            commandLine(
                "git",
                "ls-files",
                "--others",
                "--exclude-standard",
                "--",
                "android",
                "shared",
            )
            isIgnoreExitValue = true
        }
        .standardOutput
        .asText
        .get()
        .isBlank()
val sourceTreeClean =
    providers
        .exec {
            workingDir(rootProject.projectDir.parentFile)
            commandLine("git", "diff", "--quiet", "HEAD", "--", "android", "shared", "LICENSE")
            isIgnoreExitValue = true
        }
        .result
        .get()
        .exitValue == 0 && noUntrackedMobileSource
val gitSourceRevision =
    if (sourceTreeClean)
        providers
            .exec {
                workingDir(rootProject.projectDir.parentFile)
                commandLine("git", "rev-parse", "HEAD")
                isIgnoreExitValue = true
            }
            .standardOutput
            .asText
            .get()
            .trim()
            .takeIf { it.matches(Regex("[a-f0-9]{40}")) }
            .orEmpty()
    else ""

android {
    namespace = "zip.psst.android"
    compileSdk = libs.versions.android.compileSdk.get().toInt()
    buildToolsVersion = "37.0.0"
    testBuildType = deviceTestBuildType ?: "debug"

    defaultConfig {
        applicationId = "zip.psst.android"
        minSdk = libs.versions.android.minSdk.get().toInt()
        targetSdk = libs.versions.android.targetSdk.get().toInt()
        versionCode = androidVersionCode
        versionName = androidVersionName
        buildConfigField("String", "SOURCE_REVISION", "\"$gitSourceRevision\"")
        testInstrumentationRunner =
            if (deviceTestBuildType != null) "zip.psst.android.fixture.ReleaseUpdateInstrumentation"
            else "androidx.test.runner.AndroidJUnitRunner"
    }

    if (deviceTestBuildType != null) {
        // Compile the private diagnostic in the target so R8 sees its complete
        // store/crypto call graph. Its separate runner uses Android APIs only.
        sourceSets.getByName("main").java.srcDir("src/deviceFixture/java")
        sourceSets.getByName("androidTest").java.setSrcDirs(listOf("src/deviceFixtureTest/java"))
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
            if (deviceTestBuildType == "release") {
                // Only this reflective fixture entry stays named; app/store/
                // crypto code remains subject to whole-program optimization.
                proguardFiles("proguard-device-fixture.pro")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    // AGP 9.4.1's bundled Kotlin/UAST walker stalls in this single detector.
    // checkPsstSourceBidi provides stricter raw-source checking instead; retain
    // every other lint rule. Revisit with the next compatible stable AGP patch.
    // See docs/security/lint-bidi-workaround.md for measured evidence and limits.
    lint { disable.add("BidiSpoofing") }
}

val checkSourceBidi =
    tasks.register<Exec>("checkPsstSourceBidi") {
        workingDir(rootProject.projectDir.parentFile)
        commandLine(
            "python3",
            "tools/check_source_bidi.py",
            "--generated-directory",
            "android/app/build/generated/ksp/release/kotlin",
        )
        mustRunAfter(tasks.matching { it.name == "kspReleaseKotlin" })
    }

tasks
    .matching { it.name == "lintAnalyzeRelease" || it.name == "lintVitalAnalyzeRelease" }
    .configureEach { dependsOn(checkSourceBidi) }

// Signing is a separate, protected step. Gradle never reads a signing keystore
// or substitutes the debug key; an optimized APK must be requested explicitly.
val allowUnsignedRelease = providers.environmentVariable("PSST_ANDROID_UNSIGNED_RELEASE")

tasks
    .matching { it.name == "packageRelease" }
    .configureEach {
        onlyIf {
            check(allowUnsignedRelease.orNull == "true") {
                "Set PSST_ANDROID_UNSIGNED_RELEASE=true to build the unsigned APK for separate signing"
            }
            true
        }
    }

abstract class GeneratePsstReleaseMetadata : DefaultTask() {
    @get:Input abstract val versionName: Property<String>
    @get:Input abstract val versionCode: Property<Int>
    @get:Input abstract val sourceRevision: Property<String>
    @get:Input abstract val deviceFixture: Property<Boolean>
    @get:OutputDirectory abstract val outputDirectory: DirectoryProperty

    @TaskAction
    fun generate() {
        val destination = outputDirectory.get().file("psst-release.json").asFile
        destination.parentFile.mkdirs()
        destination.writeText(
            JsonOutput.toJson(
                buildMap {
                    put("versionName", versionName.get())
                    put("versionCode", versionCode.get())
                    put("sourceRevision", sourceRevision.get())
                    // Exact public metadata verification rejects this extra key,
                    // even if a private fixture uses the normal version code.
                    if (deviceFixture.get()) put("deviceFixture", true)
                },
            ) + "\n",
        )
    }
}

androidComponents.onVariants { variant ->
    val metadata =
        tasks.register<GeneratePsstReleaseMetadata>(
            "generate${variant.name.replaceFirstChar { it.uppercaseChar() }}PsstReleaseMetadata",
        ) {
            versionName.set(androidVersionName)
            versionCode.set(androidVersionCode)
            sourceRevision.set(gitSourceRevision)
            deviceFixture.set(deviceTestBuildType != null)
        }
    variant.sources.assets?.addGeneratedSourceDirectory(
        metadata,
        GeneratePsstReleaseMetadata::outputDirectory,
    )
}

dependencies {
    // KMP shared module
    implementation("zip.psst:shared")

    // Compose BOM
    val composeBom = platform(libs.compose.bom)
    implementation(composeBom)

    implementation(libs.compose.ui)
    implementation(libs.compose.ui.graphics)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons)
    debugImplementation(libs.compose.ui.tooling)

    // Activity & Lifecycle
    implementation(libs.activity.compose)
    implementation(libs.appcompat)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.lifecycle.viewmodel.compose)

    // Navigation
    implementation(libs.navigation.compose)

    // Room
    implementation(libs.room.runtime)
    implementation(libs.room.ktx)
    ksp(libs.room.compiler)

    // Coroutines
    implementation(libs.kotlinx.coroutines.android)

    if (deviceTestBuildType == null) {
        androidTestImplementation(libs.android.test.runner)
        androidTestImplementation(libs.android.test.junit)
    }

    testImplementation("junit:junit:4.13.2")
    testImplementation(libs.sqlite.jdbc)
    testImplementation(libs.ktor.client.mock)
    testImplementation(libs.ktor.client.content.negotiation)
    testImplementation(libs.ktor.serialization.json)
    testImplementation(libs.kotlinx.coroutines.test)

    // QR code generation
    implementation(libs.zxing.core)
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
}

kotlin { compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) } }
