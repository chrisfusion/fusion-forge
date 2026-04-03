package fusion.forge.api.dto;

import java.util.List;

public class VenvBuildPageResponse {

    public List<VenvBuildResponse> items;
    public long total;
    public int  page;
    public int  pageSize;

    public VenvBuildPageResponse(List<VenvBuildResponse> items, long total, int page, int pageSize) {
        this.items    = items;
        this.total    = total;
        this.page     = page;
        this.pageSize = pageSize;
    }
}
