package fusion.forge.build.service;

import fusion.forge.api.dto.VenvBuildPageResponse;
import fusion.forge.api.mapper.VenvBuildMapper;
import fusion.forge.build.entity.VenvBuild;
import fusion.forge.build.enums.BuildStatus;
import fusion.forge.build.k8s.KubernetesJobService;
import fusion.forge.client.IndexBackendClient;
import fusion.forge.client.dto.IndexCreateJobRequest;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import jakarta.ws.rs.NotFoundException;
import jakarta.ws.rs.WebApplicationException;
import jakarta.ws.rs.core.Response;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

import java.nio.charset.StandardCharsets;
import java.util.Set;

@ApplicationScoped
public class VenvBuildService {

    private static final Logger LOG = Logger.getLogger(VenvBuildService.class);

    private static final Set<String> ALLOWED_SORT_FIELDS =
            Set.of("createdAt", "updatedAt", "name", "version", "status");

    @Inject
    @RestClient
    IndexBackendClient indexBackendClient;

    @Inject
    TemplateInitService templateInitService;

    @Inject
    KubernetesJobService k8sJobService;

    @Inject
    VenvBuildMapper mapper;

    @ConfigProperty(name = "forge.builder.image")
    String builderImage;

    @Transactional
    public VenvBuildPageResponse list(int page, int pageSize,
                                       BuildStatus status, String name, String creatorId,
                                       String sortBy, String sortDir) {
        if (!ALLOWED_SORT_FIELDS.contains(sortBy)) sortBy = "createdAt";
        if (!"asc".equalsIgnoreCase(sortDir))       sortDir = "desc";
        pageSize = Math.min(pageSize, 100);

        var query = VenvBuild.listWithFilters(status, name, creatorId, sortBy, sortDir)
                             .page(page, pageSize);
        long total = query.count();
        var items  = query.list().stream().map(mapper::toResponse).toList();
        return new VenvBuildPageResponse(items, total, page, pageSize);
    }

    /**
     * Initiates an async venv build. External calls (index-backend, K8s) run outside
     * any transaction to prevent partial-failure rollbacks from masking orphaned resources.
     * The DB row is committed as PENDING first; failures update it to FAILED.
     */
    public VenvBuild initiate(String name, String version, String description,
                               String creatorId, String creatorEmail,
                               byte[] requirementsBytes) {
        // Decode once with explicit charset
        String requirementsTxt = new String(requirementsBytes, StandardCharsets.UTF_8);

        VenvBuild build = createPending(name, version, description, creatorId, creatorEmail);

        try {
            long templateVersionId = templateInitService.getTemplateVersionId();
            var indexJob = indexBackendClient.createJob(new IndexCreateJobRequest(
                    name + ":" + version,
                    description,
                    templateVersionId,
                    builderImage,
                    "venv://forge",
                    version,
                    requirementsTxt));

            var k8sResources = k8sJobService.createBuildResources(
                    build.id, indexJob.id, name + "-" + version, requirementsTxt);

            markBuilding(build.id, indexJob.id, k8sResources.jobName(), k8sResources.configMapName());
        } catch (Exception e) {
            markFailed(build.id);
            LOG.errorf("Build initiation failed for %s:%s — %s", name, version, e.getMessage());
            throw new WebApplicationException("Build initiation failed: " + e.getMessage(),
                    Response.Status.INTERNAL_SERVER_ERROR);
        }

        return findById(build.id);
    }

    @Transactional
    public VenvBuild findById(Long id) {
        return VenvBuild.<VenvBuild>findByIdOptional(id)
                .orElseThrow(() -> new NotFoundException("Venv build not found: " + id));
    }

    public String getLogs(Long id) {
        VenvBuild build = findById(id);
        if (build.k8sJobName == null) return null;
        return k8sJobService.getPodLogs(build.id);
    }

    @Transactional
    VenvBuild createPending(String name, String version, String description,
                              String creatorId, String creatorEmail) {
        if (VenvBuild.findByNameAndVersion(name, version).isPresent()) {
            throw new WebApplicationException(
                    "A venv package '" + name + ":" + version + "' already exists.",
                    Response.Status.CONFLICT);
        }
        VenvBuild build = new VenvBuild();
        build.name         = name;
        build.version      = version;
        build.description  = description;
        build.creatorId    = creatorId;
        build.creatorEmail = creatorEmail;
        build.status       = BuildStatus.PENDING;
        build.persist();
        LOG.infof("Created pending build id=%d for %s:%s", build.id, name, version);
        return build;
    }

    @Transactional
    void markBuilding(Long id, Long indexBackendJobId, String k8sJobName, String configMapName) {
        VenvBuild build = findById(id);
        build.indexBackendJobId = indexBackendJobId;
        build.k8sJobName        = k8sJobName;
        build.k8sConfigMapName  = configMapName;
        build.status            = BuildStatus.BUILDING;
    }

    @Transactional
    void markFailed(Long id) {
        VenvBuild.<VenvBuild>findByIdOptional(id).ifPresent(b -> b.status = BuildStatus.FAILED);
    }
}
