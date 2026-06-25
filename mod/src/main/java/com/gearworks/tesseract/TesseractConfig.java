package com.gearworks.tesseract;

import net.neoforged.neoforge.common.ModConfigSpec;

/**
 * Connection settings for the external tesseract-service.
 *
 * <p>Each value can be overridden by an environment variable (handy in Docker), otherwise it
 * falls back to the config file ({@code config/tesseract-common.toml}).
 */
public final class TesseractConfig {

    public static final ModConfigSpec SPEC;
    public static final ModConfigSpec.ConfigValue<String> SERVICE_HOST;
    public static final ModConfigSpec.IntValue SERVICE_PORT;
    public static final ModConfigSpec.ConfigValue<String> SERVER_NAME;

    static {
        ModConfigSpec.Builder b = new ModConfigSpec.Builder();
        b.push("tesseract");
        SERVICE_HOST = b.comment("Hostname of the tesseract sync service (env: TESSERACT_SERVICE_HOST)")
                .define("serviceHost", "tesseract-service");
        SERVICE_PORT = b.comment("TCP port of the tesseract sync service (env: TESSERACT_SERVICE_PORT)")
                .defineInRange("servicePort", 7600, 1, 65535);
        SERVER_NAME = b.comment("Unique name identifying THIS server in the HELLO handshake (env: TESSERACT_SERVER_NAME)")
                .define("serverName", "server-1");
        b.pop();
        SPEC = b.build();
    }

    private TesseractConfig() {}

    public static String getServiceHost() {
        String env = System.getenv("TESSERACT_SERVICE_HOST");
        return (env != null && !env.isEmpty()) ? env : SERVICE_HOST.get();
    }

    public static int getServicePort() {
        String env = System.getenv("TESSERACT_SERVICE_PORT");
        if (env != null && !env.isEmpty()) return Integer.parseInt(env);
        return SERVICE_PORT.get();
    }

    public static String getServerName() {
        String env = System.getenv("TESSERACT_SERVER_NAME");
        return (env != null && !env.isEmpty()) ? env : SERVER_NAME.get();
    }
}
