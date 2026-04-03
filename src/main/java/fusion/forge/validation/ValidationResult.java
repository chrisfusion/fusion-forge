package fusion.forge.validation;

import java.util.List;

public record ValidationResult(boolean valid, List<Violation> violations) {

    public static ValidationResult ok() {
        return new ValidationResult(true, List.of());
    }

    public static ValidationResult of(List<Violation> violations) {
        return new ValidationResult(violations.isEmpty(), violations);
    }
}
