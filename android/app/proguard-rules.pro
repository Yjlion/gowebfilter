# Keep the gomobile-generated bindings — they are called reflectively/JNI
# across the Go<->Java boundary and must not be stripped or renamed.
-keep class mobile.** { *; }
-keep class go.** { *; }
