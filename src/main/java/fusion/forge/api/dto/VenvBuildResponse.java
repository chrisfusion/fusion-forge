package fusion.forge.api.dto;

import fusion.forge.build.enums.BuildStatus;

import java.time.Instant;

public class VenvBuildResponse {

    public Long        id;
    public String      name;
    public String      version;
    public String      description;
    public BuildStatus status;
    public String      creatorId;
    public String      creatorEmail;
    public Long        indexBackendJobId;
    public String      k8sJobName;
    public Instant     createdAt;
    public Instant     updatedAt;
}
