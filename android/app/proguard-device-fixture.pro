# The framework-only test runner reflects into this target-contained diagnostic.
# Keep its single entry signature, while optimizing the complete fixture call graph.
# Public releases omit both this source directory and this rule.
-keep,allowoptimization class zip.psst.android.data.ReleaseUpdateFixture {
    public static void run(android.content.Context, android.os.Bundle);
}
