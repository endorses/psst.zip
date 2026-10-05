package zip.psst.shared.api

import kotlin.contracts.ExperimentalContracts
import kotlin.contracts.contract

/** Presentation metadata, independent of language and never inferred from diagnostic prose. */
interface ClientFailure {
    val failureCode: String
    val failureArguments: Map<String, String>
}

class FailureDescription(code: String, arguments: Map<String, String> = emptyMap()) {
    val code: String = code.also { require(it.matches(Regex("[a-z][a-z0-9_]{0,63}"))) }
    val arguments: Map<String, String> =
        arguments
            .also {
                require(it.size <= 8)
                require(
                    it.all { (key, value) ->
                        key.matches(Regex("[a-z][a-z0-9_]{0,31}")) &&
                            value.length <= 256 &&
                            value.none { char -> char.code in 0..31 || char.code in 127..159 }
                    }
                )
            }
            .toMap()
}

open class ClientFailureException(
    message: String,
    code: String,
    arguments: Map<String, String> = emptyMap(),
) : IllegalArgumentException(message), ClientFailure {
    private val description = FailureDescription(code, arguments)
    override val failureCode: String
        get() = description.code

    override val failureArguments: Map<String, String>
        get() = description.arguments
}

class ClientStateFailureException(
    message: String,
    code: String,
    arguments: Map<String, String> = emptyMap(),
    cause: Throwable? = null,
) : IllegalStateException(message, cause), ClientFailure {
    private val description = FailureDescription(code, arguments)
    override val failureCode: String
        get() = description.code

    override val failureArguments: Map<String, String>
        get() = description.arguments
}

/**
 * Exposed to both native presenters. Unknown network/server prose is intentionally not returned.
 */
object FailureDescriptions {
    fun describe(error: Throwable): FailureDescription? {
        if (error is kotlinx.coroutines.CancellationException) return null
        var current: Throwable? = error
        repeat(4) {
            val candidate = current ?: return null
            if (candidate is kotlinx.coroutines.CancellationException) return null
            if (candidate is ClientFailure) {
                return FailureDescription(candidate.failureCode, candidate.failureArguments)
            }
            current = candidate.cause.takeIf { it !== candidate }
        }
        return null
    }
}

@OptIn(ExperimentalContracts::class)
internal inline fun clientRequire(
    value: Boolean,
    code: String,
    arguments: Map<String, String> = emptyMap(),
    message: () -> String,
) {
    contract { returns() implies value }
    if (!value) throw ClientFailureException(message(), code, arguments)
}

@OptIn(ExperimentalContracts::class)
internal inline fun clientCheck(
    value: Boolean,
    code: String,
    arguments: Map<String, String> = emptyMap(),
    message: () -> String,
) {
    contract { returns() implies value }
    if (!value) throw ClientStateFailureException(message(), code, arguments)
}
