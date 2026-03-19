pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolution {
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "psst-android"

include(":app")

// Include the KMP shared module as a composite build
includeBuild("../shared") {
    dependencySubstitution {
        substitute(module("zip.psst:shared")).using(project(":"))
    }
}
