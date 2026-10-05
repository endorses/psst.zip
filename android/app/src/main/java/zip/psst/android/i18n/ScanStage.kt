package zip.psst.android.i18n

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import zip.psst.android.R

/** Operational stages are independent of their localized captions. */
enum class ScanStage(val caption: Int) {
    NONE(R.string.stage_none),
    SAVED(R.string.stage_saved),
    RESUMABLE(R.string.stage_resumable),
    CANCELLED(R.string.stage_cancelled),
    INSPECTING(R.string.stage_inspecting),
    MISSING(R.string.stage_missing),
    CONFIRM(R.string.stage_confirm),
    DOWNLOADING(R.string.stage_downloading),
    DECRYPTING(R.string.stage_decrypting),
    SAVING(R.string.stage_saving),
    AVAILABLE_SAVED(R.string.stage_available_saved),
    PAUSED(R.string.stage_paused),
    STORAGE(R.string.stage_storage),
    INTERRUPTED(R.string.stage_interrupted),
    RECEIPTS(R.string.stage_receipts),
    CLEANUP(R.string.stage_cleanup),
    CHECKING(R.string.stage_checking),
    PREPARING(R.string.stage_preparing),
    ENCRYPTING(R.string.stage_encrypting),
    UPLOADING(R.string.stage_uploading),
    SENT(R.string.stage_sent),
    UPLOAD_CANCELLED(R.string.stage_upload_cancelled),
    SELECTION(R.string.stage_selection),
    UNAVAILABLE(R.string.stage_unavailable),
    BUDGET(R.string.stage_budget);

    @Composable fun label(): String = stringResource(caption)

    companion object {
        fun forFailure(error: Throwable): ScanStage =
            when ((error as? zip.psst.shared.api.ClientFailure)?.failureCode) {
                "traffic_budget_exhausted" -> BUDGET
                "public_transfers_paused",
                "resource_revoked" -> UNAVAILABLE
                else -> INTERRUPTED
            }
    }
}
