package zip.psst.android

import android.app.Application
import zip.psst.android.data.AppDatabase
import zip.psst.android.data.PrefsManager

class PsstApplication : Application() {

    lateinit var database: AppDatabase
        private set

    lateinit var prefs: PrefsManager
        private set

    override fun onCreate() {
        super.onCreate()
        zip.psst.android.i18n.UiStrings.application = this
        // No upload runs before Application startup; remove snapshots left by process termination.
        cacheDir
            .listFiles()
            ?.filter { it.name.startsWith("psst-upload-") && it.name.endsWith(".partial") }
            ?.forEach { it.delete() }
        database = AppDatabase.create(this)
        prefs = PrefsManager(this)
    }
}
