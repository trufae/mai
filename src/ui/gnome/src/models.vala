using GLib;
using Json;

public struct Tool {
    public string name;
    public string? description;

    public Tool.from_json(Json.Object obj) {
        name = obj.get_string_member("name");
        description = obj.get_string_member("description");
    }
}

public struct Prompt {
    public string name;
    public string? description;

    public Prompt.from_json(Json.Object obj) {
        name = obj.get_string_member("name");
        description = obj.get_string_member("description");
    }
}

public struct Resource {
    public string uri;
    public string? name;
    public string? description;
    public string? mime_type;

    public Resource.from_json(Json.Object obj) {
        uri = obj.get_string_member("uri");
        name = obj.get_string_member("name");
        description = obj.get_string_member("description");
        mime_type = obj.get_string_member("mimeType");
    }
}