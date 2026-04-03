package fusion.forge.client.dto;

public class IndexCreateJobRequest {

    public String name;
    public String description;
    public long   templateVersionId;
    public String dockerImage;
    public String gitUrl;
    public String gitRef;
    public String runConfig;

    public IndexCreateJobRequest() {}

    public IndexCreateJobRequest(String name, String description, long templateVersionId,
                                  String dockerImage, String gitUrl, String gitRef,
                                  String runConfig) {
        this.name              = name;
        this.description       = description;
        this.templateVersionId = templateVersionId;
        this.dockerImage       = dockerImage;
        this.gitUrl            = gitUrl;
        this.gitRef            = gitRef;
        this.runConfig         = runConfig;
    }
}
