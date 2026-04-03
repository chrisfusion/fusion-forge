package fusion.forge.api.dto;

import fusion.forge.validation.ValidationResult;
import fusion.forge.validation.Violation;

import java.util.List;

public record ValidationResponse(boolean valid, List<Violation> violations) {

    public static ValidationResponse from(ValidationResult result) {
        return new ValidationResponse(result.valid(), result.violations());
    }
}
