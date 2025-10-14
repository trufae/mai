using Gtk;
using Adw;

public class SettingsWindow : Gtk.Box {
    private Gtk.DropDown provider_combo;
    private Gtk.DropDown model_combo;
    private Gtk.Entry baseurl_entry;
    private Gtk.CheckButton deterministic_check;
    private MCPClient mcp_client;

    public SettingsWindow (MCPClient client) {
        Object (orientation: Gtk.Orientation.VERTICAL, spacing: 12, margin_start: 12, margin_end: 12, margin_top: 12, margin_bottom: 12);
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

        var baseurl_label = new Gtk.Label ("Base URL:");
        baseurl_entry = new Gtk.Entry ();

        var deterministic_label = new Gtk.Label ("Deterministic:");
        deterministic_check = new Gtk.CheckButton ();

        // Provider row
        var provider_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 12);
        provider_label.hexpand = false;
        provider_combo.hexpand = true;
        provider_box.append (provider_label);
        provider_box.append (provider_combo);
        append (provider_box);

        // Model row
        var model_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 12);
        model_label.hexpand = false;
        model_combo.hexpand = true;
        model_box.append (model_label);
        model_box.append (model_combo);
        append (model_box);

        // Base URL row
        var baseurl_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 12);
        baseurl_label.hexpand = false;
        baseurl_entry.hexpand = true;
        baseurl_box.append (baseurl_label);
        baseurl_box.append (baseurl_entry);
        append (baseurl_box);

        // Deterministic row
        var deterministic_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 12);
        deterministic_check.hexpand = false;
        deterministic_label.hexpand = false;
        deterministic_box.append (deterministic_check);
        deterministic_box.append (deterministic_label);
        append (deterministic_box);

        provider_combo.notify["selected"].connect (on_provider_changed);
        model_combo.notify["selected"].connect (on_model_changed);
        baseurl_entry.activate.connect (() => {
            var args = new HashTable<string, Value?> (str_hash, str_equal);
            args["key"] = "ai.baseurl";
            args["value"] = baseurl_entry.text;
            mcp_client.call_tool.begin ("set_config", args, (obj, res) => {
                try {
                    var result = mcp_client.call_tool.end (res);
                } catch (Error e) {
                    stdout.printf ("SettingsWindow.set_baseurl: set_config error: %s\n", e.message);
                }
            });
        });
        deterministic_check.toggled.connect (() => {
            var args = new HashTable<string, Value?> (str_hash, str_equal);
            args["key"] = "ai.deterministic";
            args["value"] = deterministic_check.active;
            mcp_client.call_tool.begin ("set_config", args, (obj, res) => {
                try {
                    var result = mcp_client.call_tool.end (res);
                } catch (Error e) {
                    stdout.printf ("SettingsWindow.set_deterministic: set_config error: %s\n", e.message);
                }
            });
        });

        // Load providers and models from MCP
        load_providers ();
        load_models ();
        sync_current_config ();
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
                stdout.printf ("SettingsWindow.load_providers: Error: %s\n", e.message);
            }
        });
    }

    private void sync_current_config () {
        mcp_client.call_tool.begin ("list_config", new HashTable<string, Value?> (str_hash, str_equal), (obj, res) => {
            try {
                var result = mcp_client.call_tool.end (res);
                var parser = new Json.Parser ();
                parser.load_from_data (result);
                var root = parser.get_root ();
                if (root != null && root.get_node_type () == Json.NodeType.OBJECT) {
                    var config_obj = root.get_object ();
                    var provider = config_obj.get_string_member ("ai.provider");
                    var model = config_obj.get_string_member ("ai.model");
                    var baseurl = config_obj.get_string_member ("ai.baseurl");
                    var deterministic = config_obj.get_boolean_member ("ai.deterministic");

                    // Set selected provider
                    if (provider != null) {
                        var provider_model = provider_combo.model as Gtk.StringList;
                        for (uint i = 0; i < provider_model.get_n_items (); i++) {
                            var item = provider_model.get_string (i);
                            if (item == provider) {
                                provider_combo.selected = i;
                                break;
                            }
                        }
                    }

                    // Set selected model
                    if (model != null) {
                        var model_model = model_combo.model as Gtk.StringList;
                        for (uint i = 0; i < model_model.get_n_items (); i++) {
                            var item = model_model.get_string (i);
                            if (item == model) {
                                model_combo.selected = i;
                                break;
                            }
                         }
                     }

                      // Set baseurl
                      if (baseurl != null) {
                          baseurl_entry.text = baseurl;
                      }

                      // Set deterministic
                      deterministic_check.active = deterministic;
                 }
             } catch (Error e) {
                 stdout.printf ("SettingsWindow.sync_current_config: Error: %s\n", e.message);
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
                    try {
                        var result = mcp_client.call_tool.end (res);
                        load_models ();
                    } catch (Error e) {
                        stdout.printf ("SettingsWindow.on_provider_changed: set_provider error: %s\n", e.message);
                    }
                });
            }
        }
    }

    private void on_model_changed () {
        var selected = model_combo.selected;
        if (selected >= 0) {
            var model = model_combo.model as Gtk.StringList;
            if (model != null) {
                var model_id = model.get_string (selected);
                var args = new HashTable<string, Value?> (str_hash, str_equal);
                args["model"] = model_id;
                mcp_client.call_tool.begin ("set_model", args, (obj, res) => {
                    try {
                        var result = mcp_client.call_tool.end (res);
                    } catch (Error e) {
                        stdout.printf ("SettingsWindow.on_model_changed: set_model error: %s\n", e.message);
                    }
                });
            }
        }
    }

    private void load_models () {
        mcp_client.call_tool.begin ("get_models", new HashTable<string, Value?> (str_hash, str_equal), (obj, res) => {
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
                    string? current_model = null;
                    for (var i = 0; i < models_array.get_length (); i++) {
                        var model_obj = models_array.get_object_element (i);
                        if (model_obj != null) {
                            var model_id = model_obj.get_string_member ("id");
                            var is_current = model_obj.get_boolean_member ("current");
                            if (model_id != null) {
                                model_model.append (model_id);
                                if (is_current) {
                                    current_model = model_id;
                                }
                            }
                        }
                    }
                    // Set selected model if we found a current one
                    if (current_model != null) {
                        for (uint i = 0; i < model_model.get_n_items (); i++) {
                            var item = model_model.get_string (i);
                            if (item == current_model) {
                                model_combo.selected = i;
                                break;
                            }
                        }
                    }
                }
            } catch (Error e) {
                stdout.printf ("SettingsWindow.load_models: Error: %s\n", e.message);
            }
        });
    }
}