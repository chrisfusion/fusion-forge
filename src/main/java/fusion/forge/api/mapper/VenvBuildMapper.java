package fusion.forge.api.mapper;

import fusion.forge.api.dto.VenvBuildResponse;
import fusion.forge.build.entity.VenvBuild;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class VenvBuildMapper {

    public VenvBuildResponse toResponse(VenvBuild b) {
        VenvBuildResponse r = new VenvBuildResponse();
        r.id                = b.id;
        r.name              = b.name;
        r.version           = b.version;
        r.description       = b.description;
        r.status            = b.status;
        r.creatorId         = b.creatorId;
        r.creatorEmail      = b.creatorEmail;
        r.indexBackendJobId = b.indexBackendJobId;
        r.k8sJobName        = b.k8sJobName;
        r.createdAt         = b.createdAt;
        r.updatedAt         = b.updatedAt;
        return r;
    }
}
