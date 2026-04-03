package fusion.forge.client.dto;

public class IndexCreateTemplateRequest {

    public String name;
    public String description;
    public String dockerImage;

    public IndexCreateTemplateRequest() {}

    public IndexCreateTemplateRequest(String name, String description, String dockerImage) {
        this.name        = name;
        this.description = description;
        this.dockerImage = dockerImage;
    }
}
