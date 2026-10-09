# AndroidJUnitRunner uses the target's tracing dependency, which AGP omits from
# the separate test APK. Preserve this facade for the disposable update fixture;
# normal production releases do not load this rule and retain full R8 shrinking.
-keep,allowoptimization class androidx.tracing.Trace { *; }
