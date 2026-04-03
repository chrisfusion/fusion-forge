package fusion.forge.build.service;

import fusion.forge.client.IndexBackendClient;
import fusion.forge.client.dto.IndexCreateTemplateRequest;
import fusion.forge.client.dto.IndexTemplateResponse;
import io.quarkus.runtime.StartupEvent;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

import java.util.concurrent.atomic.AtomicLong;

/**
 * Ensures the "venv-builder" job template exists in index-backend on startup
 * and caches its templateVersionId for use in job creation.
 */
@ApplicationScoped
public class TemplateInitService {

    private static final Logger LOG = Logger.getLogger(TemplateInitService.class);
    private static final String TEMPLATE_NAME = "venv-builder";

    @Inject
    @RestClient
    IndexBackendClient indexBackendClient;

    @ConfigProperty(name = "forge.builder.image")
    String builderImage;

    private final AtomicLong templateVersionId = new AtomicLong(-1);

    void onStart(@Observes StartupEvent ev) {
        try {
            ensureTemplate();
        } catch (Exception e) {
            LOG.warnf("Could not initialize venv-builder template in index-backend: %s — builds will fail until resolved", e.getMessage());
        }
    }

    public long getTemplateVersionId() {
        if (templateVersionId.get() < 0) {
            ensureTemplate();
        }
        return templateVersionId.get();
    }

    private synchronized void ensureTemplate() {
        // Find existing template by name
        var page = indexBackendClient.listTemplates(0, 100);
        var existing = page.items.stream()
                .filter(t -> TEMPLATE_NAME.equals(t.name))
                .findFirst();

        IndexTemplateResponse template;
        if (existing.isPresent()) {
            template = existing.get();
            LOG.debugf("Found existing venv-builder template id=%d", template.id);
        } else {
            template = indexBackendClient.createTemplate(
                    new IndexCreateTemplateRequest(TEMPLATE_NAME, "Python 3.12 venv builder", builderImage));
            LOG.infof("Created venv-builder template id=%d", template.id);
        }

        var versions = indexBackendClient.listTemplateVersions(template.id);
        if (versions.isEmpty()) {
            throw new IllegalStateException("Template " + template.id + " has no versions");
        }
        long versionId = versions.get(0).id;
        templateVersionId.set(versionId);
        LOG.infof("Template version id=%d cached", versionId);
    }
}
