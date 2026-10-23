import groovy.json.JsonOutput
import groovy.json.JsonSlurper
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import java.util.zip.ZipInputStream
import javax.xml.XMLConstants
import javax.xml.parsers.DocumentBuilderFactory
import org.gradle.api.artifacts.component.ModuleComponentIdentifier
import org.gradle.api.artifacts.result.ResolvedArtifactResult
import org.gradle.api.provider.ValueSource
import org.gradle.api.provider.ValueSourceParameters
import org.gradle.maven.MavenModule
import org.gradle.maven.MavenPomArtifact
import org.w3c.dom.Element

// One Gradle-owned implementation for both native dependency graphs. It uses
// only the Gradle/JDK runtime; standalone Android/F-Droid builds need no Python
// or Go. These are explicit maintenance tasks, never dependency-resolution work
// performed during ordinary APK assembly.
object PsstNativeTooling {
    const val MAX_FILE = 1024 * 1024
    const val MAX_SOURCE = 8 * 1024 * 1024
    const val MAX_TOTAL = 64 * 1024 * 1024
    const val MAX_FILES = 10000
    val sourceSuffixes = setOf("kt", "kts", "java", "gradle", "swift")
    val bidi = (0x202a..0x202e).toSet() + (0x2066..0x2069).toSet()
    val unicodeEscape =
        Regex("""\\(?:u+([0-9a-f]{4})|u\{([0-9a-f]{1,8})\})""", RegexOption.IGNORE_CASE)
    val legalName =
        Regex(
            "(?:^|/)(?:LICENSE|LICENCE|NOTICE|COPYRIGHT|COPYING)(?:[^/]*)$",
            RegexOption.IGNORE_CASE,
        )
    val inputs =
        listOf(
            "android/app/build.gradle.kts",
            "android/gradle/libs.versions.toml",
            "android/settings.gradle.kts",
            "shared/build.gradle.kts",
            "shared/gradle/libs.versions.toml",
            "shared/settings.gradle.kts",
            "shared/licenses/source-inventory.json",
            "shared/gradle/native-tooling.gradle.kts",
        )
    val wrapperDeletions =
        listOf("android", "shared")
            .flatMap { project ->
                listOf("gradlew", "gradlew.bat", "gradle/wrapper/gradle-wrapper.jar").map {
                    "$project/$it"
                }
            }
            .toSet()

    fun requireSafe(condition: Boolean, message: String) {
        if (!condition) throw GradleException(message)
    }

    fun utf8(data: ByteArray): String =
        StandardCharsets.UTF_8.newDecoder()
            .onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT)
            .decode(ByteBuffer.wrap(data))
            .toString()

    fun sha(data: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(data).joinToString("") { "%02x".format(it) }

    fun normalize(data: ByteArray): ByteArray =
        (utf8(data)
                .replace("\r\n", "\n")
                .replace('\r', '\n')
                .split('\n')
                .joinToString("\n") { it.trimEnd(' ', '\t') }
                .trimEnd('\n') + "\n")
            .toByteArray(StandardCharsets.UTF_8)

    fun readBounded(path: Path, limit: Int): ByteArray {
        requireSafe(
            Files.isRegularFile(path, NOFOLLOW_LINKS),
            "Native tooling input is not a regular file",
        )
        return Files.newInputStream(path, StandardOpenOption.READ, NOFOLLOW_LINKS).use {
            val data = it.readNBytes(limit + 1)
            requireSafe(data.size <= limit, "Native tooling input exceeds size bounds")
            data
        }
    }

    fun checkedPath(root: Path, candidate: Path): Path {
        val path = candidate.toAbsolutePath()
        requireSafe(
            path.startsWith(root) && root.relativize(path).none { it.toString() == ".." },
            "Source path must stay within checkout",
        )
        var current = root
        root.relativize(path).forEach { part ->
            current = current.resolve(part)
            requireSafe(!Files.isSymbolicLink(current), "Source paths must not contain symlinks")
        }
        return path
    }

    // Drain stdout concurrently so neither a full pipe nor a stuck Git process
    // can hang Gradle. Ignore stderr to keep diagnostics free of source values.
    fun git(root: Path, arguments: List<String>, limit: Int = MAX_SOURCE): String {
        val process =
            ProcessBuilder(listOf("git", "-C", root.toString()) + arguments)
                .redirectError(ProcessBuilder.Redirect.DISCARD)
                .start()
        val output = ByteArrayOutputStream()
        var failure: Throwable? = null
        val reader = Thread {
            try {
                process.inputStream.use { input ->
                    val buffer = ByteArray(65536)
                    while (true) {
                        val count = input.read(buffer)
                        if (count < 0) break
                        requireSafe(
                            output.size() + count <= limit,
                            "Tracked inventory exceeds bounds",
                        )
                        output.write(buffer, 0, count)
                    }
                }
            } catch (error: Throwable) {
                failure = error
                process.destroyForcibly()
            }
        }
        reader.isDaemon = true
        reader.start()
        try {
            requireSafe(
                process.waitFor(15, TimeUnit.SECONDS),
                "Source Git command exceeded its time limit",
            )
            reader.join(1000)
            requireSafe(
                !reader.isAlive && failure == null && process.exitValue() == 0,
                "Source Git command failed",
            )
            return utf8(output.toByteArray())
        } finally {
            if (process.isAlive) process.destroyForcibly()
            reader.join(1000)
        }
    }

    fun sourceRevision(root: Path): String =
        try {
            val revision = git(root, listOf("rev-parse", "--verify", "HEAD^{commit}")).trim()
            if (!revision.matches(Regex("[a-f0-9]{40}"))) ""
            else {
                val status =
                    git(
                        root,
                        listOf(
                            "--no-optional-locks",
                            "status",
                            "--porcelain=v1",
                            "-z",
                            "--untracked-files=all",
                            "--",
                            "android",
                            "shared",
                            "LICENSE",
                        ),
                    )
                if (
                    status.split('\u0000').all {
                        it.isEmpty() ||
                            (it.startsWith(" D ") && it.substring(3) in wrapperDeletions)
                    }
                )
                    revision
                else ""
            }
        } catch (_: Exception) {
            // Debug/exported source builds without clean Git metadata get no exact
            // source URL, rather than an identity which claims modified source.
            ""
        }

    fun firstBidi(text: String): Pair<Int, Int>? {
        text.lineSequence().forEachIndexed { index, line ->
            line
                .firstOrNull { it.code in bidi }
                ?.let {
                    return (index + 1) to it.code
                }
            unicodeEscape.findAll(line).forEach { match ->
                val codepoint = (match.groups[1]?.value ?: match.groups[2]!!.value).toLong(16)
                if (codepoint <= Int.MAX_VALUE && codepoint.toInt() in bidi)
                    return (index + 1) to codepoint.toInt()
            }
        }
        return null
    }

    fun checkBidi(root: Path, generated: List<Path>, includeTracked: Boolean = true): Int {
        val inventory = if (includeTracked) git(root, listOf("ls-files", "--cached", "-z")) else ""
        requireSafe(
            inventory.isEmpty() || inventory.endsWith('\u0000'),
            "Tracked inventory is truncated",
        )
        val names = inventory.trimEnd('\u0000').split('\u0000').filter { it.isNotEmpty() }
        requireSafe(names.size <= MAX_FILES, "Tracked inventory contains too many files")
        val sources =
            names
                .filter { it.substringAfterLast('.', "").lowercase() in sourceSuffixes }
                .map {
                    requireSafe(
                        !Path.of(it).isAbsolute,
                        "Tracked inventory contains an absolute path",
                    )
                    checkedPath(root, root.resolve(it))
                }
                .toMutableSet()
        requireSafe(
            !includeTracked || sources.isNotEmpty(),
            "Tracked inventory contains no applicable sources",
        )
        generated.forEach { raw ->
            val directory = checkedPath(root, raw)
            val parts = root.relativize(directory).map { it.toString() }
            requireSafe(
                parts.windowed(2).any { it == listOf("build", "generated") } &&
                    directory.fileName.toString() in setOf("kotlin", "java", "swift"),
                "Select emitted source beneath build/generated",
            )
            requireSafe(
                Files.isDirectory(directory, NOFOLLOW_LINKS),
                "Generated source directory is unavailable",
            )
            var entries = 0
            fun visit(parent: Path, depth: Int) {
                requireSafe(depth <= 32, "Generated directory depth exceeds bounds")
                Files.newDirectoryStream(parent).use { children ->
                    children.forEach { child ->
                        requireSafe(
                            ++entries <= MAX_FILES,
                            "Generated directory inventory exceeds bounds",
                        )
                        requireSafe(
                            !Files.isSymbolicLink(child),
                            "Generated source must not contain symlinks",
                        )
                        if (Files.isDirectory(child, NOFOLLOW_LINKS)) visit(child, depth + 1)
                        else if (
                            child.fileName.toString().substringAfterLast('.', "").lowercase() in
                                sourceSuffixes
                        )
                            sources.add(child)
                    }
                }
            }
            visit(directory, 0)
        }
        requireSafe(sources.size <= MAX_FILES, "Source file count exceeds bounds")
        var total = 0
        sources.sorted().forEach { path ->
            val data = readBounded(checkedPath(root, path), minOf(MAX_SOURCE, MAX_TOTAL - total))
            total += data.size
            requireSafe(total <= MAX_TOTAL, "Combined source size exceeds bounds")
            firstBidi(utf8(data))?.let { (line, codepoint) ->
                throw GradleException(
                    "Bidi formatting control: ${JsonOutput.toJson(root.relativize(path).toString())}:$line U+${"%04X".format(codepoint)}",
                )
            }
        }
        return sources.size
    }

    @Suppress("UNCHECKED_CAST")
    fun json(path: Path): Map<String, Any?> =
        JsonSlurper().parse(readBounded(path, MAX_SOURCE)) as Map<String, Any?>

    // Keep the checked-in JSON readable and deterministic, including compact
    // empty arrays/maps. Groovy's pretty-printer adds blank indented lines for
    // empty collections, which both churns these snapshots and fails whitespace
    // checks. Primitive quoting still uses Gradle's JSON encoder.
    fun jsonBytes(value: Any): ByteArray {
        fun encode(item: Any?, depth: Int): String {
            val prefix = "  ".repeat(depth + 1)
            val suffix = "  ".repeat(depth)
            return when (item) {
                is Map<*, *> ->
                    if (item.isEmpty()) "{}"
                    else
                        item.entries.joinToString(
                            separator = ",\n",
                            prefix = "{\n",
                            postfix = "\n$suffix}",
                        ) { (key, child) ->
                            "$prefix${JsonOutput.toJson(key as String)}: ${encode(child, depth + 1)}"
                        }
                is List<*> ->
                    if (item.isEmpty()) "[]"
                    else
                        item.joinToString(
                            separator = ",\n",
                            prefix = "[\n",
                            postfix = "\n$suffix]",
                        ) { child ->
                            "$prefix${encode(child, depth + 1)}"
                        }
                else -> JsonOutput.toJson(item)
            }
        }
        return (encode(value, 0) + "\n").toByteArray(StandardCharsets.UTF_8)
    }

    @Suppress("UNCHECKED_CAST")
    fun sourceSnapshots(root: Path): Pair<Map<String, Any?>, Map<String, ByteArray>> {
        val inventory = json(root.resolve("shared/licenses/source-inventory.json"))
        val catalog = utf8(readBounded(root.resolve("shared/gradle/libs.versions.toml"), MAX_FILE))
        listOf("kotlin", "skie").forEach { name ->
            val matches = Regex("(?m)^${name}\\s*=\\s*\"([^\"]+)\"\\s*$").findAll(catalog).toList()
            requireSafe(
                matches.size == 1 &&
                    inventory["${name}_version"] == matches.single().groupValues[1],
                "Native compiler/runtime snapshots need explicit version refresh",
            )
        }
        val base = root.resolve("shared/licenses/upstream")
        val texts = linkedMapOf<String, ByteArray>()
        (inventory["sources"] as List<Map<String, Any?>>).forEach { item ->
            val name = item["file"] as String
            val data = readBounded(checkedPath(base, base.resolve(name)), MAX_FILE)
            requireSafe(
                data.isNotEmpty() &&
                    data.any { !it.toInt().toChar().isWhitespace() } &&
                    sha(data) == item["sha256"],
                "Native upstream notice drift",
            )
            requireSafe(
                (item["source"] as String).startsWith("https://"),
                "Native source snapshots require HTTPS provenance",
            )
            requireSafe(texts.put(name, data) == null, "Duplicate native source snapshot")
        }
        return inventory to texts
    }

    fun legalFiles(data: ByteArray, nested: Boolean = false): Map<String, ByteArray> {
        requireSafe(
            data.size >= 4 &&
                data[0] == 0x50.toByte() &&
                data[1] == 0x4b.toByte() &&
                ((data[2] == 3.toByte() && data[3] == 4.toByte()) ||
                    (data[2] == 5.toByte() && data[3] == 6.toByte())),
            "Invalid native dependency archive",
        )
        val result = linkedMapOf<String, ByteArray>()
        ZipInputStream(ByteArrayInputStream(data)).use { archive ->
            var count = 0
            while (true) {
                val entry = archive.nextEntry ?: break
                requireSafe(++count <= 100000, "Excessive native archive entry count")
                val name = entry.name
                if (!entry.isDirectory) {
                    val extension = name.substringAfterLast('.', "").lowercase()
                    if (
                        legalName.containsMatchIn(name) &&
                            extension !in
                                setOf(
                                    "class",
                                    "jar",
                                    "so",
                                    "pdf",
                                    "png",
                                    "zip",
                                    "dex",
                                    "kotlin_module",
                                )
                    ) {
                        requireSafe(entry.size <= MAX_FILE, "Oversized native legal input")
                        val payload = archive.readNBytes(MAX_FILE + 1)
                        requireSafe(payload.size <= MAX_FILE, "Oversized native legal input")
                        if (payload.any { !it.toInt().toChar().isWhitespace() }) {
                            requireSafe(
                                result.put(name, payload) == null,
                                "Duplicate native legal entry",
                            )
                        }
                    } else if (!nested && name.endsWith(".jar")) {
                        val limit = 256 * 1024 * 1024
                        requireSafe(entry.size <= limit, "Oversized nested native archive")
                        val payload = archive.readNBytes(limit + 1)
                        requireSafe(payload.size <= limit, "Oversized nested native archive")
                        legalFiles(payload, true).forEach { (child, text) ->
                            result["$name!/$child"] = text
                        }
                    }
                }
                archive.closeEntry()
            }
        }
        return result
    }

    fun xml(data: ByteArray): Element {
        val factory = DocumentBuilderFactory.newInstance()
        factory.isNamespaceAware = true
        factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true)
        factory.setFeature("http://xml.org/sax/features/external-general-entities", false)
        factory.setFeature("http://xml.org/sax/features/external-parameter-entities", false)
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD, "")
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "")
        return factory.newDocumentBuilder().parse(ByteArrayInputStream(data)).documentElement
    }

    fun children(element: Element, name: String): List<Element> =
        (0 until element.childNodes.length).mapNotNull { index ->
            val child = element.childNodes.item(index)
            (child as? Element)?.takeIf { (it.localName ?: it.nodeName) == name }
        }

    fun text(element: Element, name: String): String? =
        children(element, name).firstOrNull()?.textContent?.takeIf { it.isNotEmpty() }

    data class Pom(
        val licenses: List<Map<String, String>>,
        val source: String?,
        val inputs: List<Map<String, String>>,
        val copyrights: List<String>,
    )

    fun pomMetadata(paths: List<Path>): Pom {
        var licenses = emptyList<Map<String, String>>()
        var source: String? = null
        val inputs = mutableListOf<Map<String, String>>()
        val copyrights = mutableListOf<String>()
        paths.forEach { path ->
            val data = readBounded(path, MAX_FILE)
            val tree = xml(data)
            inputs.add(linkedMapOf("name" to path.fileName.toString(), "sha256" to sha(data)))
            val current =
                children(tree, "licenses")
                    .flatMap { children(it, "license") }
                    .map {
                        linkedMapOf(
                            "name" to (text(it, "name") ?: ""),
                            "url" to (text(it, "url") ?: ""),
                        )
                    }
            if (licenses.isEmpty() && current.isNotEmpty()) licenses = current
            if (source == null)
                source =
                    children(tree, "scm").firstOrNull()?.let { text(it, "url") }
                        ?: text(tree, "url")
            Regex("<!--[\\s\\S]*?-->").findAll(utf8(data)).forEach { match ->
                val comment = match.value.removePrefix("<!--").removeSuffix("-->")
                if (comment.contains("copyright", ignoreCase = true))
                    copyrights.add(utf8(normalize(comment.toByteArray())).trim())
            }
        }
        requireSafe(licenses.isNotEmpty(), "Native dependency has no effective declared license")
        return Pom(licenses, source, inputs, copyrights)
    }

    fun effectiveLicense(coordinate: String, licenses: List<Map<String, String>>): String {
        val names = licenses.map { it["name"] }.toSet()
        requireSafe(names.size == 1, "Review multiple native licenses")
        val name = names.single()
        if (
            name in
                setOf(
                    "Apache-2.0",
                    "Apache 2.0",
                    "Apache License, Version 2.0",
                    "The Apache License, Version 2.0",
                    "The Apache Software License, Version 2.0",
                )
        )
            return "Apache-2.0"
        if (coordinate == "org.slf4j:slf4j-api:2.0.19" && name == "MIT") return "MIT"
        throw GradleException("Unreviewed native dependency license/copyright: $coordinate")
    }

    fun inputHashes(root: Path): Map<String, String> = inputs.associateWith {
        sha(readBounded(root.resolve(it), MAX_FILE))
    }

    @Suppress("UNCHECKED_CAST")
    fun generate(
        root: Path,
        platform: String,
        records: List<Map<String, Any?>>,
    ): Map<String, ByteArray> {
        val (sourceInventory, texts) = sourceSnapshots(root)
        val license = readBounded(root.resolve("LICENSE"), MAX_FILE)
        val notices =
            mutableListOf(
                "psst.zip native third-party notices",
                "Scope: $platform; exact resolved library artifact variants below, plus pinned compiler-injected runtimes.",
                "License snapshots normalize line endings and trailing spaces only; source hashes retain the original upstream bytes.",
            )
        val components = mutableListOf<Map<String, Any?>>()
        val seen = mutableSetOf<List<String>>()
        records
            .sortedWith(
                compareBy(
                    { it["group"] as String },
                    { it["name"] as String },
                    { it["version"] as String },
                    { it["configuration"] as String },
                    { Path.of(it["file"] as String).fileName.toString() },
                ),
            )
            .forEach { record ->
                val coordinate = "${record["group"]}:${record["name"]}:${record["version"]}"
                val artifact = Path.of(record["file"] as String)
                val name = artifact.fileName.toString()
                val configuration = record["configuration"] as String
                requireSafe(
                    seen.add(listOf(coordinate, configuration, name)),
                    "Duplicate native artifact variant",
                )
                val pom = pomMetadata((record["poms"] as List<String>).map { Path.of(it) })
                val spdx = effectiveLicense(coordinate, pom.licenses)
                val data = readBounded(artifact, 256 * 1024 * 1024)
                val embedded = mutableListOf<Map<String, String>>()
                notices.addAll(
                    listOf(
                        "",
                        coordinate,
                        "Artifact: $name",
                        "License: $spdx",
                        "Source: ${pom.source ?: "See published Maven POM"}",
                    ),
                )
                notices.addAll(pom.copyrights)
                legalFiles(data).toSortedMap().forEach { (path, payload) ->
                    embedded.add(
                        linkedMapOf(
                            "path" to path,
                            "sha256" to sha(normalize(payload)),
                            "upstream_sha256" to sha(payload),
                        ),
                    )
                    notices.addAll(
                        listOf("Embedded upstream legal file: $path", utf8(normalize(payload))),
                    )
                }
                if (spdx == "MIT")
                    notices.addAll(
                        listOf(
                            "Full upstream MIT license and copyright:",
                            utf8(texts.getValue("slf4j-2.0.19/LICENSE.txt")),
                        ),
                    )
                components.add(
                    linkedMapOf(
                        "coordinate" to coordinate,
                        "configuration" to configuration,
                        "artifact" to name,
                        "artifact_sha256" to sha(data),
                        "license" to spdx,
                        "declared_licenses" to pom.licenses,
                        "source" to pom.source,
                        "pom_inputs" to pom.inputs,
                        "embedded_notices" to embedded,
                    ),
                )
            }
        notices.addAll(
            listOf(
                "",
                "Full POM-declared Apache License, Version 2.0:",
                utf8(texts.getValue("apache-2.0/LICENSE.txt")),
            ),
        )
        val extra =
            (sourceInventory["sources"] as List<Map<String, Any?>>).filter {
                val name = it["file"] as String
                name.startsWith("kotlin-") ||
                    (platform == "ios" &&
                        (name.startsWith("kotlin-native-") || name.startsWith("skie-")))
            }
        extra.forEach { item ->
            notices.addAll(
                listOf(
                    "",
                    item["component"] as String,
                    "Upstream legal file: ${item["file"]}",
                    "Exact source: ${item["source"]}",
                    utf8(texts.getValue(item["file"] as String)),
                ),
            )
        }
        val noticeBytes = normalize(notices.joinToString("\n").toByteArray())
        val inventory =
            linkedMapOf(
                "schema_version" to 1,
                "platform" to platform,
                "scope" to
                    if (platform == "android") "Android release runtime"
                    else
                        "iOS arm64, simulator arm64 and x64 compile klibs; Kotlin/Native and SKIE runtime",
                "input_sha256" to inputHashes(root),
                "components" to components,
                "runtime_notice_sources" to extra,
                "notices_complete_for_scope" to true,
                "notices_sha256" to sha(noticeBytes),
                "project_license_sha256" to sha(license),
                "excluded" to
                    listOf(
                        "Project Shared framework (covered by AGPL)",
                        "Build-only compiler/plugin code",
                        "Apple/Android platform SDK frameworks supplied by the operating system",
                    ),
            )
        return linkedMapOf(
            "AGPL-3.0-only.txt" to license,
            "dependency-inventory.json" to jsonBytes(inventory),
            "THIRD_PARTY_NOTICES.txt" to noticeBytes,
        )
    }

    fun checkInputs(root: Path, platform: String, destination: Path) {
        sourceSnapshots(root)
        val inventory = json(destination.resolve("dependency-inventory.json"))
        requireSafe(
            inventory["input_sha256"] == inputHashes(root),
            "Stale native inventory build inputs: $platform",
        )
        requireSafe(
            sha(readBounded(destination.resolve("THIRD_PARTY_NOTICES.txt"), MAX_SOURCE)) ==
                inventory["notices_sha256"],
            "Native aggregate notices drift: $platform",
        )
        requireSafe(
            readBounded(destination.resolve("AGPL-3.0-only.txt"), MAX_FILE)
                .contentEquals(readBounded(root.resolve("LICENSE"), MAX_FILE)),
            "Native project license drift: $platform",
        )
    }
}

// ValueSource is Gradle's supported boundary for bounded configuration-time Git
// identity reads. A direct ProcessBuilder here is deprecated by Gradle 9.8.
abstract class PsstSourceRevision : ValueSource<String, PsstSourceRevision.Parameters> {
    interface Parameters : ValueSourceParameters {
        val checkout: DirectoryProperty
    }

    override fun obtain(): String =
        PsstNativeTooling.sourceRevision(parameters.checkout.get().asFile.toPath())
}

val repositoryRoot = rootProject.projectDir.parentFile.toPath().toAbsolutePath().normalize()
val platform = if (project.path == ":app") "android" else "ios"
val destination =
    repositoryRoot.resolve(
        if (platform == "android") "android/app/src/main/assets/licenses"
        else "ios/Shared/LegalResources",
    )

if (platform == "android")
    extra["psstSourceRevision"] =
        providers
            .of(PsstSourceRevision::class.java) {
                parameters.checkout.set(rootProject.projectDir.parentFile)
            }
            .get()

val legalConfigurations =
    if (platform == "android") listOf("releaseRuntimeClasspath")
    else
        listOf(
            "iosArm64CompileKlibraries",
            "iosSimulatorArm64CompileKlibraries",
            "iosX64CompileKlibraries",
        )

fun resolveLegalArtifacts(): List<Map<String, Any?>> {
    fun queryPom(group: String, module: String, version: String): Path {
        val result =
            dependencies
                .createArtifactResolutionQuery()
                .forModule(group, module, version)
                .withArtifacts(MavenModule::class.java, MavenPomArtifact::class.java)
                .execute()
        val artifact =
            result.resolvedComponents
                .flatMap { it.getArtifacts(MavenPomArtifact::class.java) }
                .filterIsInstance<ResolvedArtifactResult>()
                .firstOrNull()
        return artifact?.file?.toPath()
            ?: throw GradleException("Missing native dependency POM: $group:$module:$version")
    }
    val poms = mutableMapOf<List<String>, List<String>>()
    val records = mutableListOf<Map<String, Any?>>()
    legalConfigurations.forEach { name ->
        configurations
            .getByName(name)
            .incoming
            .artifactView { componentFilter { it is ModuleComponentIdentifier } }
            .artifacts
            .artifacts
            .forEach { artifact ->
                val id = artifact.id.componentIdentifier as ModuleComponentIdentifier
                val coordinate = listOf(id.group, id.module, id.version)
                val parents =
                    poms.getOrPut(coordinate) {
                        var file = queryPom(id.group, id.module, id.version)
                        val seen = mutableSetOf<Path>()
                        val chain = mutableListOf<String>()
                        for (depth in 0 until 8) {
                            PsstNativeTooling.requireSafe(
                                seen.add(file),
                                "Cyclic native POM parent",
                            )
                            chain.add(file.toString())
                            val parent =
                                PsstNativeTooling.children(
                                        PsstNativeTooling.xml(
                                            PsstNativeTooling.readBounded(
                                                file,
                                                PsstNativeTooling.MAX_FILE,
                                            ),
                                        ),
                                        "parent",
                                    )
                                    .firstOrNull() ?: break
                            PsstNativeTooling.requireSafe(
                                depth < 7,
                                "Native POM parent depth exceeds limit",
                            )
                            file =
                                queryPom(
                                    PsstNativeTooling.text(parent, "groupId") ?: "",
                                    PsstNativeTooling.text(parent, "artifactId") ?: "",
                                    PsstNativeTooling.text(parent, "version") ?: "",
                                )
                        }
                        chain
                    }
                records.add(
                    linkedMapOf(
                        "configuration" to name,
                        "group" to id.group,
                        "name" to id.module,
                        "version" to id.version,
                        "file" to artifact.file.absolutePath,
                        "poms" to parents,
                    ),
                )
            }
    }
    return records
        .distinctBy {
            listOf(it["group"], it["name"], it["version"], it["configuration"], it["file"])
        }
        .sortedWith(
            compareBy(
                { it["group"] as String },
                { it["name"] as String },
                { it["version"] as String },
                { it["configuration"] as String },
                { it["file"] as String },
            ),
        )
}

val exportLegalArtifacts =
    tasks.register("psstExportLegalArtifacts") {
        group = "verification"
        description =
            "Export exact Android runtime or iOS compile-klib artifacts and parent POM associations."
        doLast {
            val records = resolveLegalArtifacts()
            val folder =
                providers.gradleProperty("psst.legalOutput").orNull?.let { file(it) }
                    ?: layout.buildDirectory.dir("native-legal").get().asFile
            folder.mkdirs()
            folder.resolve("$platform.json").writeBytes(PsstNativeTooling.jsonBytes(records))
            logger.lifecycle(
                "Resolved native legal inputs: ${records.size} artifacts for $platform",
            )
        }
    }

tasks.register("generatePsstNativeNotices") {
    group = "build setup"
    description = "Regenerate checked-in native legal resources from exact resolved artifacts."
    doLast {
        val records = resolveLegalArtifacts()
        Files.createDirectories(destination)
        PsstNativeTooling.generate(repositoryRoot, platform, records).forEach { (name, data) ->
            Files.write(destination.resolve(name), data)
        }
        logger.lifecycle(
            "Native notices generated: $platform, ${records.size} exact artifact variants",
        )
    }
}

tasks.register("checkPsstNativeNotices") {
    group = "verification"
    description = "Verify exact resolved artifacts and generated native notices."
    doLast {
        val records = resolveLegalArtifacts()
        PsstNativeTooling.generate(repositoryRoot, platform, records).forEach { (name, data) ->
            PsstNativeTooling.requireSafe(
                Files.exists(destination.resolve(name)) &&
                    Files.readAllBytes(destination.resolve(name)).contentEquals(data),
                "Stale native notices: $platform/$name",
            )
        }
        logger.lifecycle(
            "Native notices verified: $platform, ${records.size} exact artifact variants",
        )
    }
}

tasks.register("checkPsstNativeNoticeInputs") {
    group = "verification"
    description =
        "Check snapshot, source/build and bundled legal bytes without resolving dependencies."
    doLast {
        PsstNativeTooling.checkInputs(repositoryRoot, platform, destination)
        logger.lifecycle(
            "Native notice source/build freshness passed: $platform (artifact resolution not run)",
        )
    }
}

if (platform == "android") {
    val checkBidi =
        tasks.register("checkPsstSourceBidi") {
            group = "verification"
            description =
                "Reject literal/escaped bidi formatting controls without Kotlin PSI traversal."
            doLast {
                val generated =
                    providers.gradleProperty("psst.bidiGeneratedDirectory").orNull?.let {
                        listOf(repositoryRoot.resolve(it))
                    } ?: emptyList()
                try {
                    logger.lifecycle(
                        "Bidi source check passed: ${PsstNativeTooling.checkBidi(repositoryRoot, generated)} tracked/generated source files.",
                    )
                } catch (error: GradleException) {
                    throw error
                } catch (_: Exception) {
                    throw GradleException(
                        "Bidi source check failed: unavailable source/inventory or invalid UTF-8.",
                    )
                }
            }
        }
    // Generated compilation input is required when release lint executes. Direct
    // tracked-only invocation remains useful before KSP has generated anything.
    val checkGeneratedBidi =
        tasks.register("checkPsstGeneratedSourceBidi") {
            dependsOn(checkBidi)
            mustRunAfter(tasks.matching { it.name == "kspReleaseKotlin" })
            doLast {
                PsstNativeTooling.checkBidi(
                    repositoryRoot,
                    listOf(
                        repositoryRoot.resolve("android/app/build/generated/ksp/release/kotlin"),
                    ),
                    includeTracked = false,
                )
            }
        }
    tasks
        .matching { it.name == "lintAnalyzeRelease" || it.name == "lintVitalAnalyzeRelease" }
        .configureEach { dependsOn(checkGeneratedBidi) }
}

// Small behavioral maintenance tests; no Android compilation, network, compiler
// plugins, signing material or dependencies beyond the existing Gradle/JDK.
tasks.register("testPsstNativeTooling") {
    group = "verification"
    description =
        "Exercise source identity, Bidi and legal-input trust boundaries using disposable fixtures."
    doLast {
        val root = Files.createTempDirectory("psst-native-tooling-test-")
        fun assertTrue(value: Boolean) = check(value) { "Native tooling regression failed" }
        fun rejects(action: () -> Unit) {
            var rejected = false
            try {
                action()
            } catch (_: Exception) {
                rejected = true
            }
            assertTrue(rejected)
        }
        fun write(name: String, value: String): Path {
            val path = root.resolve(name)
            Files.createDirectories(path.parent)
            Files.writeString(path, value)
            return path
        }
        fun zip(files: Map<String, ByteArray>): ByteArray {
            val bytes = ByteArrayOutputStream()
            java.util.zip.ZipOutputStream(bytes).use { output ->
                files.forEach { (name, data) ->
                    output.putNextEntry(java.util.zip.ZipEntry(name))
                    output.write(data)
                    output.closeEntry()
                }
            }
            return bytes.toByteArray()
        }
        try {
            PsstNativeTooling.wrapperDeletions.forEach { write(it, "wrapper fixture\n") }
            val source = write("android/app/Main.kt", "// Grüße und 日本語\n")
            write("shared/src/Shared.kt", "// shared fixture\n")
            write("LICENSE", "license fixture\n")
            PsstNativeTooling.git(root, listOf("init", "-q"))
            PsstNativeTooling.git(root, listOf("add", "."))
            PsstNativeTooling.git(
                root,
                listOf(
                    "-c",
                    "user.name=Native Tooling Test",
                    "-c",
                    "user.email=native-tooling@example.invalid",
                    "-c",
                    "commit.gpgsign=false",
                    "commit",
                    "-qm",
                    "fixture",
                ),
            )
            val revision = PsstNativeTooling.sourceRevision(root)
            assertTrue(revision.matches(Regex("[a-f0-9]{40}")))
            PsstNativeTooling.wrapperDeletions.forEach {
                Files.delete(root.resolve(it))
                assertTrue(PsstNativeTooling.sourceRevision(root) == revision)
            }
            write("notes.txt", "unrelated\n")
            assertTrue(PsstNativeTooling.sourceRevision(root) == revision)
            val untracked = write("shared/untracked.kt", "untracked\n")
            assertTrue(PsstNativeTooling.sourceRevision(root).isEmpty())
            Files.delete(untracked)
            Files.writeString(source, "// modified\n")
            assertTrue(PsstNativeTooling.sourceRevision(root).isEmpty())
            PsstNativeTooling.git(root, listOf("reset", "--hard", "HEAD"))
            PsstNativeTooling.git(root, listOf("rm", "android/gradlew"))
            assertTrue(PsstNativeTooling.sourceRevision(root).isEmpty())
            PsstNativeTooling.git(root, listOf("reset", "--hard", "HEAD"))
            assertTrue(PsstNativeTooling.checkBidi(root, emptyList()) == 2)
            val escape = 92.toChar().toString()
            val controls =
                listOf("// " + 0x202e.toChar()) +
                    listOf("u202A", "uuuu202e", "u{2066}", "U2069", escape + "u202e").map {
                        "// " + escape + it
                    }
            controls.forEach { text ->
                Files.writeString(source, "// safe\n$text\n")
                assertTrue(PsstNativeTooling.firstBidi(Files.readString(source))?.first == 2)
                rejects { PsstNativeTooling.checkBidi(root, emptyList()) }
            }
            Files.write(source, byteArrayOf(0xff.toByte()))
            rejects { PsstNativeTooling.checkBidi(root, emptyList()) }
            Files.writeString(source, "// safe\n")
            val generated =
                write(
                    "android/app/build/generated/ksp/release/kotlin/Dao.kt",
                    "// " + 0x2067.toChar(),
                )
            rejects { PsstNativeTooling.checkBidi(root, listOf(generated.parent)) }
            rejects { PsstNativeTooling.checkBidi(root, listOf(root.resolve("android/app/build"))) }
            Files.writeString(generated, "// safe\n")
            assertTrue(PsstNativeTooling.checkBidi(root, listOf(generated.parent)) == 3)
            Files.delete(source)
            Files.createSymbolicLink(source, generated)
            rejects { PsstNativeTooling.checkBidi(root, emptyList()) }
            val jar =
                zip(
                    mapOf(
                        "META-INF/LICENSE.txt" to
                            "Copyright Alice\r\nLicense text  \r\n".toByteArray(),
                        "Notice.class" to byteArrayOf(1),
                    ),
                )
            val legal =
                PsstNativeTooling.legalFiles(
                    zip(mapOf("classes.jar" to jar, "NOTICE" to "Upstream notice".toByteArray())),
                )
            assertTrue(legal.keys == setOf("classes.jar!/META-INF/LICENSE.txt", "NOTICE"))
            assertTrue(
                PsstNativeTooling.utf8(
                    PsstNativeTooling.normalize(
                        legal.getValue("classes.jar!/META-INF/LICENSE.txt"),
                    ),
                ) == "Copyright Alice\nLicense text\n",
            )
            rejects {
                PsstNativeTooling.legalFiles(
                    zip(mapOf("LICENSE" to ByteArray(PsstNativeTooling.MAX_FILE + 1) { 120 })),
                )
            }
            rejects { PsstNativeTooling.legalFiles("not a zip".toByteArray()) }
            val mit = listOf(mapOf("name" to "MIT", "url" to "https://opensource.org/license/mit"))
            assertTrue(
                PsstNativeTooling.effectiveLicense("org.slf4j:slf4j-api:2.0.19", mit) == "MIT",
            )
            rejects { PsstNativeTooling.effectiveLicense("example:unknown:1", mit) }
            rejects { PsstNativeTooling.effectiveLicense("org.slf4j:slf4j-api:2.0.17", mit) }
            val child =
                write(
                    "child.pom",
                    "<project xmlns=\"urn:maven\"><scm><url>https://example.org/exact-source</url></scm></project>",
                )
            val parent =
                write(
                    "parent.pom",
                    "<!-- Copyright 2026 Alice --><project xmlns=\"urn:maven\"><licenses><license><name>Apache-2.0</name><url>https://www.apache.org/licenses/LICENSE-2.0.txt</url></license></licenses></project>",
                )
            val metadata = PsstNativeTooling.pomMetadata(listOf(child, parent))
            assertTrue(
                metadata.source == "https://example.org/exact-source" &&
                    metadata.inputs.size == 2 &&
                    metadata.copyrights == listOf("Copyright 2026 Alice"),
            )
            rejects { PsstNativeTooling.pomMetadata(listOf(child)) }
            val (snapshots, texts) = PsstNativeTooling.sourceSnapshots(repositoryRoot)
            write(
                "shared/licenses/source-inventory.json",
                PsstNativeTooling.utf8(PsstNativeTooling.jsonBytes(snapshots)),
            )
            write(
                "shared/gradle/libs.versions.toml",
                Files.readString(repositoryRoot.resolve("shared/gradle/libs.versions.toml")),
            )
            texts.forEach { (name, data) ->
                write("shared/licenses/upstream/$name", PsstNativeTooling.utf8(data))
            }
            PsstNativeTooling.sourceSnapshots(root)
            write("shared/licenses/upstream/apache-2.0/LICENSE.txt", "changed\n")
            rejects { PsstNativeTooling.sourceSnapshots(root) }
            logger.lifecycle(
                "Native tooling boundary checks passed: source identity/F-Droid, Bidi, archives, POM inheritance, license allowlist and snapshot drift.",
            )
        } finally {
            Files.walk(root).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.delete(it) }
            }
        }
    }
}
