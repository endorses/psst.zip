package zip.psst.android.ui.components

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.hardware.Camera
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import zip.psst.android.R
import zip.psst.android.i18n.*
import com.google.zxing.*
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.multi.qrcode.QRCodeMultiReader
import com.journeyapps.barcodescanner.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Camera lifetime is bounded by this visible composable and the navigation entry lifecycle. */
@Composable
fun EmbeddedScanner(onCode: (String) -> Unit, onError: (UiText) -> Unit) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()
    val latestCode by rememberUpdatedState(onCode)
    val latestError by rememberUpdatedState(onError)
    var granted by remember {
        mutableStateOf(
            ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) ==
                PackageManager.PERMISSION_GRANTED
        )
    }
    val permissionPreferences = remember {
        context.getSharedPreferences("scanner-permissions", android.content.Context.MODE_PRIVATE)
    }
    var requested by rememberSaveable {
        mutableStateOf(permissionPreferences.getBoolean("camera-requested", false))
    }
    var detected by remember { mutableStateOf(false) }
    var cameraError by remember { mutableStateOf(false) }
    var processingImage by remember { mutableStateOf(false) }
    val cameraCount = remember { runCatching { Camera.getNumberOfCameras() }.getOrDefault(0) }
    val cameraId = remember {
        selectScannerCamera(
            (0 until cameraCount).mapNotNull { id ->
                runCatching {
                        val info = Camera.CameraInfo()
                        Camera.getCameraInfo(id, info)
                        id to (info.facing == Camera.CameraInfo.CAMERA_FACING_BACK)
                    }
                    .getOrNull()
            }
        )
    }
    var cameraGeneration by remember { mutableIntStateOf(0) }
    var torchSupported by remember { mutableStateOf(false) }
    var torch by remember { mutableStateOf(false) }
    val camera =
        remember(cameraId, cameraGeneration) {
            BarcodeView(context).apply {
                decoderFactory = DefaultDecoderFactory(listOf(BarcodeFormat.QR_CODE))
                cameraSettings.requestedCameraId = cameraId ?: -1
            }
        }
    fun stop() {
        torch = false
        torchSupported = false
        camera.setTorch(false)
        camera.stopDecoding()
        camera.pause()
    }
    val permission =
        rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
            granted = it
        }
    LaunchedEffect(Unit) {
        if (!granted && !requested && cameraCount > 0) {
            requested = true
            permissionPreferences.edit().putBoolean("camera-requested", true).apply()
            permission.launch(Manifest.permission.CAMERA)
        }
    }
    DisposableEffect(camera) {
        val listener =
            object : CameraPreview.StateListener {
                override fun previewSized() {}

                override fun previewStarted() {
                    camera.cameraInstance?.changeCameraParameters { parameters ->
                        torchSupported =
                            parameters.supportedFlashModes?.contains(
                                Camera.Parameters.FLASH_MODE_TORCH
                            ) == true
                        parameters
                    }
                }

                override fun previewStopped() {}

                override fun cameraClosed() {}

                override fun cameraError(error: Exception) {
                    cameraError = true
                    latestError(
                        message(
                            R.string
                                .l_camera_unavailable_try_again_or_choose_an_image_or_paste_a_link_96df6c
                        )
                    )
                }
            }
        camera.addStateListener(listener)
        onDispose { stop() }
    }
    DisposableEffect(camera, granted, detected, processingImage, cameraError, lifecycle) {
        fun resume() {
            if (
                cameraId != null &&
                    granted &&
                    !detected &&
                    !processingImage &&
                    !cameraError &&
                    lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)
            ) {
                camera.decodeSingle(
                    object : BarcodeCallback {
                        override fun barcodeResult(result: BarcodeResult) {
                            if (!detected) {
                                detected = true
                                stop()
                                latestCode(result.text)
                            }
                        }

                        override fun possibleResultPoints(points: MutableList<ResultPoint>?) {}
                    }
                )
                camera.resume()
            } else stop()
        }
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                granted =
                    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) ==
                        PackageManager.PERMISSION_GRANTED
                resume()
            } else if (event == Lifecycle.Event.ON_PAUSE || event == Lifecycle.Event.ON_STOP) stop()
        }
        lifecycle.addObserver(observer)
        resume()
        onDispose {
            lifecycle.removeObserver(observer)
            stop()
        }
    }
    val imagePicker =
        rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri ->
            if (uri != null) {
                stop()
                processingImage = true
                scope.launch {
                    try {
                        val decoded =
                            withContext(Dispatchers.Default) {
                                decodeQrImage(context.contentResolver, uri)
                            }
                        detected = true
                        latestCode(decoded)
                    } catch (_: Exception) {
                        latestError(
                            message(
                                R.string
                                    .l_choose_an_image_containing_exactly_one_readable_qr_code_5102b7
                            )
                        )
                    } finally {
                        processingImage = false
                    }
                }
            }
        }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (granted && cameraId != null && !detected && !cameraError && !processingImage) {
            key(cameraGeneration) {
                AndroidView(
                    factory = { camera },
                    modifier =
                        Modifier.fillMaxWidth()
                            .heightIn(min = 180.dp, max = 300.dp)
                            .aspectRatio(1.3f),
                )
            }
            Text(
                tr(R.string.l_point_the_camera_at_a_psst_zip_qr_code_1a7f37),
                style = MaterialTheme.typography.bodySmall,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (torchSupported)
                    TextButton(
                        onClick = {
                            torch = !torch
                            camera.setTorch(torch)
                        }
                    ) {
                        Text(
                            if (torch) tr(R.string.l_torch_off_29a2dd)
                            else tr(R.string.l_torch_on_83015b)
                        )
                    }
            }
        } else if (processingImage) LinearProgressIndicator(Modifier.fillMaxWidth())
        else if (!granted) {
            Text(
                tr(
                    R.string
                        .l_camera_access_is_off_you_can_still_choose_a_qr_image_or_paste_a_l_268a42
                )
            )
            TextButton(
                onClick = {
                    context.startActivity(
                        Intent(
                            Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                            Uri.parse("package:${context.packageName}"),
                        )
                    )
                }
            ) {
                Text(tr(R.string.l_open_app_settings_6f0a71))
            }
        } else if (cameraId == null)
            Text(tr(R.string.l_no_camera_available_choose_a_qr_image_or_paste_a_link_e641ec))
        else {
            if (cameraError)
                Text(
                    tr(R.string.l_camera_unavailable_retry_choose_a_qr_image_or_paste_a_link_7cf984)
                )
            TextButton(
                onClick = {
                    stop()
                    cameraGeneration++
                    cameraError = false
                    detected = false
                }
            ) {
                Text(
                    if (cameraError) tr(R.string.l_retry_camera_d662d0)
                    else tr(R.string.l_scan_again_f6ab55)
                )
            }
        }
        OutlinedButton(
            onClick = {
                stop()
                imagePicker.launch("image/*")
            },
            enabled = !processingImage,
        ) {
            Text(tr(R.string.l_choose_qr_image_a23a18))
        }
    }
}

internal fun decodeQrImage(resolver: android.content.ContentResolver, uri: Uri): String {
    val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    resolver.openInputStream(uri).use { BitmapFactory.decodeStream(it, null, options) }
    require(options.outWidth > 0 && options.outHeight > 0)
    options.inJustDecodeBounds = false
    while (
        options.outWidth / options.inSampleSize.coerceAtLeast(1) > 2048 ||
            options.outHeight / options.inSampleSize.coerceAtLeast(1) > 2048
    ) options.inSampleSize = options.inSampleSize.coerceAtLeast(1) * 2
    val bitmap =
        resolver.openInputStream(uri).use { BitmapFactory.decodeStream(it, null, options) }
            ?: error("Invalid image")
    try {
        val pixels = IntArray(bitmap.width * bitmap.height)
        bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
        return decodeQrPixels(bitmap.width, bitmap.height, pixels)
    } finally {
        bitmap.recycle()
    }
}

internal fun decodeQrPixels(width: Int, height: Int, pixels: IntArray): String {
    val bitmap = BinaryBitmap(HybridBinarizer(RGBLuminanceSource(width, height, pixels)))
    val results =
        QRCodeMultiReader()
            .decodeMultiple(bitmap, mapOf(DecodeHintType.TRY_HARDER to true))
            .map { it.text }
            .distinct()
    require(results.size == 1)
    return results.single()
}

/** Prefer a rear camera, retaining a front-only fallback and an explicit no-camera result. */
internal fun selectScannerCamera(cameras: List<Pair<Int, Boolean>>): Int? =
    cameras.firstOrNull { it.second }?.first ?: cameras.firstOrNull()?.first
