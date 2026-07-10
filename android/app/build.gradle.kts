plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.webfilter.app"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.webfilter.app"
        minSdk = 26 // matches `gomobile bind -androidapi 26`
        targetSdk = 34
        versionCode = 1
        versionName = "0.1.0"

        // The gomobile AAR ships arm64-v8a, armeabi-v7a, and x86_64 (the
        // last one for emulators — it needs the libc seccomp patch, see
        // scripts/patch_libc_seccomp.go); keep the APK to those so we don't
        // pull in unsupported ABIs.
        ndk {
            abiFilters += listOf("arm64-v8a", "armeabi-v7a", "x86_64")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    buildFeatures {
        viewBinding = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    // webfilter.aar is produced by `gomobile bind` (see ../README.md) and
    // dropped into app/libs. It is gitignored — build it locally before
    // assembling the app.
    implementation(fileTree("libs") { include("*.aar") })

    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.constraintlayout:constraintlayout:2.1.4")
    // Native settings screens (SettingsActivity); backed by a
    // PreferenceDataStore that reads/writes the Go engine's JSON config via
    // the gomobile API instead of SharedPreferences.
    implementation("androidx.preference:preference-ktx:1.2.1")
}
