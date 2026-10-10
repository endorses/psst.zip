package zip.psst.android.fixture;

import android.app.Activity;
import android.app.Instrumentation;
import android.content.Context;
import android.os.Bundle;

/** Framework-only runner for one target-contained disposable update diagnostic. */
public final class ReleaseUpdateInstrumentation extends Instrumentation {
    private static final String FIXTURE = "zip.psst.android.data.ReleaseUpdateFixture";
    private Bundle arguments;

    @Override
    public void onCreate(Bundle arguments) {
        super.onCreate(arguments);
        this.arguments = arguments == null ? new Bundle() : new Bundle(arguments);
        start();
    }

    @Override
    public void onStart() {
        Bundle status = new Bundle();
        status.putString("id", "PsstReleaseUpdateFixture");
        status.putString("class", FIXTURE);
        status.putString("test", "run");
        status.putInt("numtests", 1);
        status.putInt("current", 1);
        sendStatus(1, status);
        Bundle result = new Bundle();
        try {
            if (!(FIXTURE + "#run").equals(arguments.getString("class"))) {
                throw new IllegalArgumentException("Select the single update diagnostic");
            }
            Context target = getTargetContext();
            target.getClassLoader()
                    .loadClass(FIXTURE)
                    .getMethod("run", Context.class, Bundle.class)
                    .invoke(null, target, arguments);
            sendStatus(0, status);
            result.putString("stream", "\nOK (1 test)\n");
            finish(Activity.RESULT_OK, result);
        } catch (Exception | LinkageError failure) {
            // Never emit exception values or persisted state, even for synthetic data.
            status.putString("stack", "Protected-state update diagnostic failed");
            sendStatus(-2, status);
            result.putString("shortMsg", "Protected-state update diagnostic failed");
            finish(Activity.RESULT_CANCELED, result);
        }
    }
}
