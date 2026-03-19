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
        database = AppDatabase.create(this)
        prefs = PrefsManager(this)
    }
}
