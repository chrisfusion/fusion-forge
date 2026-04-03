package fusion.forge.api.auth;

import jakarta.enterprise.context.RequestScoped;

@RequestScoped
public class CurrentUser {

    public String id = "anonymous";
    public String email = "anonymous@fusion.local";
}
