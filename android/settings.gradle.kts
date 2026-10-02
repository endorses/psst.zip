pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "psst-android"

include(":app")

// Include the KMP shared module as a composite build
includeBuild("../shared") {
    dependencySubstitution { substitute(module("zip.psst:shared")).using(project(":")) }
}
