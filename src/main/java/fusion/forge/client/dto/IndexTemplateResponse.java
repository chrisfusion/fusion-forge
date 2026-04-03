package fusion.forge.client.dto;

import java.time.Instant;

public class IndexTemplateResponse {

    public long    id;
    public String  name;
    public String  description;
    public String  dockerImage;
    public int     latestVersionNumber;
    public Instant createdAt;
    public Instant updatedAt;
}
