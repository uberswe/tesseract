plugins {
    id("net.neoforged.moddev") version "2.0.141"
    java
}

version = project.property("mod_version") as String
group = project.property("mod_group") as String

java.toolchain.languageVersion = JavaLanguageVersion.of(21)

neoForge {
    version = project.property("neoforge_version") as String

    runs {
        create("server") {
            server()
        }
    }

    mods {
        create("tesseract") {
            sourceSet(sourceSets.main.get())
        }
    }
}

repositories {
    mavenCentral()
    maven("https://maven.neoforged.net/releases")
}
