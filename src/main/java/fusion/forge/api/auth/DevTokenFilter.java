package fusion.forge.api.auth;

import jakarta.annotation.Priority;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerRequestFilter;
import jakarta.ws.rs.ext.Provider;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Extracts user identity from the incoming request.
 *
 * When forge.dev-token is set and the Bearer token matches, a hardcoded dev
 * user is injected into CurrentUser. This is intended for non-production
 * deployments only (set OIDC_ENABLED=false alongside FORGE_DEV_TOKEN).
 *
 * When OIDC is active, the SecurityContext principal is populated by Quarkus
 * before this filter runs, and we read the name from it.
 */
@Provider
@ApplicationScoped
@Priority(1000)
public class DevTokenFilter implements ContainerRequestFilter {

    private static final Logger LOG = Logger.getLogger(DevTokenFilter.class);

    @Inject
    CurrentUser currentUser;

    @ConfigProperty(name = "forge.dev-token", defaultValue = "")
    String devToken;

    @Override
    public void filter(ContainerRequestContext ctx) {
        String authHeader = ctx.getHeaderString("Authorization");

        if (!devToken.isBlank() && authHeader != null && authHeader.equals("Bearer " + devToken)) {
            currentUser.id    = "dev-user";
            currentUser.email = "dev@fusion.local";
            LOG.debug("Dev token matched — using dev identity");
            return;
        }

        var principal = ctx.getSecurityContext().getUserPrincipal();
        if (principal != null && principal.getName() != null) {
            currentUser.id    = principal.getName();
            currentUser.email = principal.getName();
        }
    }
}
