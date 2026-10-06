# Project guidance

## Branding

Use **psst.zip** as the exact user-facing product name across web, Android, iOS, and documentation. The brand is not a hardcoded self-hosted server address; preserve operator-configured URLs. Project identifiers use the `zip.psst` namespace and `Psst` native targets. The authorized history retcon supersedes earlier instructions to retain legacy names; subsequent changes must preserve the established psst.zip identities and protocol compatibility.

## Android and iOS parity

Every applicable mobile feature, bug fix, UX improvement, and branding change must include both Android and iOS. Provide equivalent capabilities using each platform's native conventions, including the iOS share extension when relevant. Do not defer or exclude iOS unless the user explicitly agrees to that exception.

Include both platforms in implementation plans, account for existing parity gaps needed by the work, and preserve their shared protocol compatibility. Lack of macOS, an iOS simulator, or an iOS device limits validation; it does not remove iOS implementation from scope.

Perform all implementation and verification available in the current environment. Track implementation separately from validation, leave unrun checks explicitly pending, and document the commands and environment needed to complete them. Never claim an iOS build or device flow passed when it was not tested.
