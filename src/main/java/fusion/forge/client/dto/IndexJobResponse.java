package fusion.forge.client.dto;

import java.time.Instant;

public class IndexJobResponse {

    public long    id;
    public String  name;
    public String  description;
    public long    templateVersionId;
    public int     latestVersionNumber;
    public Instant createdAt;
    public Instant updatedAt;
}
