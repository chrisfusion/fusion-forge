package fusion.forge.validation;

import com.fasterxml.jackson.annotation.JsonProperty;

import java.util.ArrayList;
import java.util.List;

public class RequirementsRules {

    @JsonProperty("require-exact-pinning")
    public boolean requireExactPinning = true;

    @JsonProperty("banned-packages")
    public List<String> bannedPackages = new ArrayList<>();

    @JsonProperty("max-packages")
    public int maxPackages = 100;
}
