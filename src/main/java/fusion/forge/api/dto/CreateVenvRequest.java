package fusion.forge.api.dto;

import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotNull;
import jakarta.validation.constraints.Pattern;
import jakarta.validation.constraints.Size;
import org.jboss.resteasy.reactive.RestForm;
import org.jboss.resteasy.reactive.multipart.FileUpload;

public class CreateVenvRequest {

    @RestForm
    @NotBlank(message = "name is required")
    @Size(max = 255, message = "name must not exceed 255 characters")
    @Pattern(regexp = "^[a-zA-Z0-9_-]+$", message = "name may only contain letters, digits, hyphens and underscores")
    public String name;

    @RestForm
    @NotBlank(message = "version is required")
    @Size(max = 255, message = "version must not exceed 255 characters")
    @Pattern(regexp = "^[a-zA-Z0-9._-]+$", message = "version may only contain letters, digits, dots, hyphens and underscores")
    public String version;

    @RestForm
    @Size(max = 2000, message = "description must not exceed 2000 characters")
    public String description;

    @RestForm
    @NotNull(message = "requirements file is required")
    public FileUpload requirements;
}
