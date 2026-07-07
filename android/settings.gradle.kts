pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.PREFER_SETTINGS)
    repositories {
        google()
        mavenCentral()
        // The gomobile-produced webfilter.aar is consumed from app/libs via a
        // fileTree dependency (see app/build.gradle.kts), so no extra repo is
        // needed for it.
    }
}

rootProject.name = "WebFilter"
include(":app")
