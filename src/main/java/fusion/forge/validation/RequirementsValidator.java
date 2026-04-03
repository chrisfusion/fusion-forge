package fusion.forge.validation;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import java.util.ArrayList;
import java.util.List;
import java.util.regex.Pattern;

/**
 * Validates requirements.txt content line by line.
 *
 * Always-on rules:
 * - Pip options (-r, --flag, -e) are rejected
 * - VCS / URL dependencies are rejected
 * - Package name must match PEP 508 naming rules
 * - A version specifier must be present (bare package names are rejected)
 *
 * Configurable rules (forge-rules.yaml):
 * - require-exact-pinning: only == is accepted as version specifier
 * - banned-packages: list of disallowed package names (case-insensitive)
 * - max-packages: upper bound on the number of requirement entries
 */
@ApplicationScoped
public class RequirementsValidator {

    // PEP 508 package name: starts and ends with alphanumeric, allows .-_ in between
    private static final Pattern PACKAGE_NAME = Pattern.compile(
            "^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$");

    // Matches a requirement line: name[extras][version_spec][env_marker]
    // Group 1: package name, Group 2: optional extras, Group 3: optional version spec
    private static final Pattern REQUIREMENT_LINE = Pattern.compile(
            "^([A-Za-z0-9][A-Za-z0-9._-]*)\\s*(\\[[^]]*])?\\s*([^;#]*)?(;.*)?$");

    // Exact pin: ==X.Y.Z (no other specifiers allowed when require-exact-pinning is true)
    private static final Pattern EXACT_PIN = Pattern.compile("^\\s*==\\s*\\S+$");

    // Any version specifier present (>=, <=, !=, ~=, >, <, ==)
    private static final Pattern ANY_SPECIFIER = Pattern.compile("[><=!~]");

    // VCS or URL dependency
    private static final Pattern VCS_OR_URL = Pattern.compile(
            "^(https?://|ftp://|git\\+|hg\\+|svn\\+|bzr\\+)", Pattern.CASE_INSENSITIVE);

    @Inject
    RulesLoader rulesLoader;

    public ValidationResult validate(String requirementsTxt) {
        RequirementsRules rules = rulesLoader.getRules();
        List<Violation> violations = new ArrayList<>();
        String[] lines = requirementsTxt.split("\\r?\\n", -1);

        int packageCount = 0;

        for (int i = 0; i < lines.length; i++) {
            int lineNumber = i + 1;
            String raw = lines[i];
            String trimmed = raw.strip();

            // Skip blanks and comments
            if (trimmed.isEmpty() || trimmed.startsWith("#")) continue;

            // Reject pip options (-r, --flag, -e editable)
            if (trimmed.startsWith("-")) {
                violations.add(new Violation(lineNumber, raw, "pip options are not allowed (found: " + trimmed.split("\\s+")[0] + ")"));
                continue;
            }

            // Reject VCS / URL dependencies
            if (VCS_OR_URL.matcher(trimmed).find()) {
                violations.add(new Violation(lineNumber, raw, "VCS and URL dependencies are not allowed"));
                continue;
            }

            // Strip inline comment for parsing
            String spec = trimmed.contains("#") ? trimmed.substring(0, trimmed.indexOf('#')).strip() : trimmed;

            var matcher = REQUIREMENT_LINE.matcher(spec);
            if (!matcher.matches()) {
                violations.add(new Violation(lineNumber, raw, "invalid pip requirement syntax"));
                continue;
            }

            String name        = matcher.group(1);
            String versionPart = matcher.group(3) != null ? matcher.group(3).strip() : "";

            // Validate package name (PEP 508)
            if (!PACKAGE_NAME.matcher(name).matches()) {
                violations.add(new Violation(lineNumber, raw, "invalid package name '" + name + "' (must match PEP 508: letters, digits, hyphens, dots, underscores)"));
                continue;
            }

            // Banned packages (case-insensitive, normalise hyphens/underscores/dots)
            String normalizedName = name.toLowerCase().replace('-', '_').replace('.', '_');
            boolean banned = false;
            for (String bannedEntry : rules.bannedPackages) {
                String normalizedBanned = bannedEntry.toLowerCase().replace('-', '_').replace('.', '_');
                if (normalizedName.equals(normalizedBanned)) {
                    violations.add(new Violation(lineNumber, raw, "package '" + name + "' is not allowed"));
                    banned = true;
                    break;
                }
            }
            if (banned) continue;

            // Version specifier checks
            boolean versionOk;
            if (versionPart.isEmpty()) {
                violations.add(new Violation(lineNumber, raw, "version specifier is required — bare package names are not allowed (e.g. use " + name + "==1.0.0)"));
                versionOk = false;
            } else if (rules.requireExactPinning && !EXACT_PIN.matcher(versionPart).matches()) {
                violations.add(new Violation(lineNumber, raw, "exact version pin required — use == (found: " + versionPart.strip() + ")"));
                versionOk = false;
            } else if (!rules.requireExactPinning && !ANY_SPECIFIER.matcher(versionPart).find()) {
                violations.add(new Violation(lineNumber, raw, "version specifier is required (found no operator in: " + versionPart.strip() + ")"));
                versionOk = false;
            } else {
                versionOk = true;
            }

            // Only count fully valid entries toward maxPackages
            if (versionOk) packageCount++;
        }

        // Max packages check (applies to valid entries counted so far)
        if (packageCount > rules.maxPackages) {
            violations.add(new Violation(0, "",
                    "too many packages: " + packageCount + " entries found, maximum is " + rules.maxPackages));
        }

        return ValidationResult.of(violations);
    }
}
