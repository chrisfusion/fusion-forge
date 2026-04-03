package fusion.forge.client.dto;

import java.util.List;

public class IndexPageResponse<T> {

    public List<T> items;
    public long    total;
    public int     page;
    public int     pageSize;
}
