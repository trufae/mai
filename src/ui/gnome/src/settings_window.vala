using Gtk;
using Adw;

public class SettingsWindow : Gtk.Box {
    private Gtk.DropDown provider_combo;
    private Gtk.DropDown model_combo;
    private MCPClient mcp_client;

    public SettingsWindow (MCPClient client) {
        Object (orientation: Gtk.Orientation.VERTICAL, spacing: 12);
        mcp_client = client;
        setup_ui ();
    }

    private void setup_ui () {
        var provider_label = new Gtk.Label ("Provider:");
        var provider_model = new Gtk.StringList (null);
        provider_combo = new Gtk.DropDown (provider_model, null);

        var model_label = new Gtk.Label ("Model:");
        var model_model = new Gtk.StringList (null);
        model_combo = new Gtk.DropDown (model_model, null);

        append (provider_label);
        append (provider_combo);
        append (model_label);
        append (model_combo);

        provider_combo.notify["selected"].connect (on_provider_changed);

        // Load providers from MCP
        load_providers ();
    }

    private void load_providers () {
        mcp_client.call_tool.begin ("list_providers", new HashTable<string, Value?> (str_hash, str_equal), (obj, res) => {
            try {
                var result = mcp_client.call_tool.end (res);
                // The result is JSON string containing the providers array
                var parser = new Json.Parser ();
                parser.load_from_data (result);
                var root = parser.get_root ();
                if (root != null && root.get_node_type () == Json.NodeType.ARRAY) {
                    var providers_array = root.get_array ();
                    var provider_model = provider_combo.model as Gtk.StringList;
                    for (var i = 0; i < providers_array.get_length (); i++) {
                        var provider = providers_array.get_string_element (i);
                        if (provider != null) {
                            provider_model.append (provider);
                        }
                    }
                }
            } catch (Error e) {
                // Handle error
            }
        });
    }

    private void on_provider_changed () {
        var selected = provider_combo.selected;
        if (selected >= 0) {
            var model = provider_combo.model as Gtk.StringList;
            if (model != null) {
                var provider = model.get_string (selected);
                var args = new HashTable<string, Value?> (str_hash, str_equal);
                args["provider"] = provider.down ();
                mcp_client.call_tool.begin ("set_provider", args, (obj, res) => {
                    // Handle response
                    load_models ();
                });
            }
        }
    }

    private void load_models () {
        mcp_client.call_tool.begin ("list_models", new HashTable<string, Value?> (str_hash, str_equal), (obj, res) => {
            try {
                var result = mcp_client.call_tool.end (res);
                // The result is JSON string containing the models array
                var parser = new Json.Parser ();
                parser.load_from_data (result);
                var root = parser.get_root ();
                if (root != null && root.get_node_type () == Json.NodeType.ARRAY) {
                    var models_array = root.get_array ();
                    var model_model = model_combo.model as Gtk.StringList;
                    // Clear existing models
                    while (model_model.get_n_items () > 0) {
                        model_model.remove (0);
                    }
                    for (var i = 0; i < models_array.get_length (); i++) {
                        var model = models_array.get_string_element (i);
                        if (model != null) {
                            model_model.append (model);
                        }
                    }
                }
            } catch (Error e) {
                // Handle error
            }
        });
    }
}