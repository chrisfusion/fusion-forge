package fusion.forge.validation;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.dataformat.yaml.YAMLFactory;
import io.quarkus.runtime.StartupEvent;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;

/**
 * Loads forge-rules.yaml at startup.
 * File path is configurable via forge.validation.rules-file (env: FORGE_RULES_FILE).
 * Falls back to the classpath default when no external path is set.
 */
@ApplicationScoped
public class RulesLoader {

    private static final Logger LOG = Logger.getLogger(RulesLoader.class);
    private static final ObjectMapper YAML = new ObjectMapper(new YAMLFactory());

    @ConfigProperty(name = "forge.validation.rules-file", defaultValue = "")
    String rulesFilePath;

    private RequirementsRules rules = new RequirementsRules();

    void onStart(@Observes StartupEvent ev) {
        rules = load();
        LOG.infof("Validation rules loaded — exactPinning=%s bannedPackages=%s maxPackages=%d",
                rules.requireExactPinning, rules.bannedPackages, rules.maxPackages);
    }

    public RequirementsRules getRules() {
        return rules;
    }

    private RequirementsRules load() {
        if (!rulesFilePath.isBlank()) {
            Path path = Path.of(rulesFilePath);
            if (Files.exists(path)) {
                try {
                    LOG.infof("Loading validation rules from external file: %s", path);
                    return YAML.readValue(path.toFile(), RequirementsRules.class);
                } catch (IOException e) {
                    LOG.warnf("Failed to load rules from %s: %s — using defaults", path, e.getMessage());
                }
            } else {
                LOG.warnf("Rules file not found at %s — using defaults", path);
            }
        }

        try (InputStream is = getClass().getClassLoader().getResourceAsStream("forge-rules.yaml")) {
            if (is != null) {
                LOG.debug("Loading validation rules from classpath forge-rules.yaml");
                return YAML.readValue(is, RequirementsRules.class);
            }
        } catch (IOException e) {
            LOG.warnf("Failed to load classpath forge-rules.yaml: %s — using built-in defaults", e.getMessage());
        }

        LOG.warn("No forge-rules.yaml found — using built-in defaults");
        return new RequirementsRules();
    }
}
