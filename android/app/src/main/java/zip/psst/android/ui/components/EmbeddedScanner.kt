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
import com.google.zxing.*
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.multi.qrcode.QRCodeMultiReader
import com.journeyapps.barcodescanner.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Camera lifetime is bounded by this visible composable and the navigation entry lifecycle. */
@Composable
fun EmbeddedScanner(onCode: (String) -> Unit, onError: (String) -> Unit) {
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
    var cameraId by remember {
        mutableIntStateOf(
            (0 until cameraCount).firstOrNull { id ->
                val info = Camera.CameraInfo()
                Camera.getCameraInfo(id, info)
                info.facing == Camera.CameraInfo.CAMERA_FACING_BACK
            } ?: 0
        )
    }
    var torch by remember { mutableStateOf(false) }
    val camera =
        remember(cameraId) {
            BarcodeView(context).apply {
                decoderFactory = DefaultDecoderFactory(listOf(BarcodeFormat.QR_CODE))
                cameraSettings.requestedCameraId = cameraId
            }
        }
    fun stop() {
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

                override fun previewStarted() {}

                override fun previewStopped() {}

                override fun cameraClosed() {}

                override fun cameraError(error: Exception) {
                    cameraError = true
                    latestError("Camera unavailable. Try again or choose an image or paste a link.")
                }
            }
        camera.addStateListener(listener)
        onDispose { stop() }
    }
    DisposableEffect(camera, granted, detected, processingImage, cameraError, lifecycle) {
        fun resume() {
            if (
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
                        latestError("Choose an image containing exactly one readable QR code.")
                    } finally {
                        processingImage = false
                    }
                }
            }
        }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (granted && cameraCount > 0 && !detected && !cameraError && !processingImage) {
            AndroidView(
                factory = { camera },
                modifier =
                    Modifier.fillMaxWidth().heightIn(min = 180.dp, max = 300.dp).aspectRatio(1.3f),
            )
            Text(
                "Point the camera at a psst.zip QR code",
                style = MaterialTheme.typography.bodySmall,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                val info = Camera.CameraInfo().also { Camera.getCameraInfo(cameraId, it) }
                if (
                    context.packageManager.hasSystemFeature(PackageManager.FEATURE_CAMERA_FLASH) &&
                        info.facing == Camera.CameraInfo.CAMERA_FACING_BACK
                )
                    TextButton(
                        onClick = {
                            torch = !torch
                            camera.setTorch(torch)
                        }
                    ) {
                        Text(if (torch) "Torch off" else "Torch on")
                    }
                if (cameraCount > 1)
                    TextButton(
                        onClick = {
                            stop()
                            torch = false
                            cameraId = (cameraId + 1) % cameraCount
                        }
                    ) {
                        Text("Switch camera")
                    }
            }
        } else if (processingImage) LinearProgressIndicator(Modifier.fillMaxWidth())
        else if (!granted) {
            Text("Camera access is off. You can still choose a QR image or paste a link.")
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
                Text("Open app settings")
            }
        } else if (cameraCount == 0) Text("No camera available. Choose a QR image or paste a link.")
        else
            TextButton(
                onClick = {
                    cameraError = false
                    detected = false
                }
            ) {
                Text("Scan again")
            }
        OutlinedButton(
            onClick = {
                stop()
                imagePicker.launch("image/*")
            },
            enabled = !processingImage,
        ) {
            Text("Choose QR image")
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
