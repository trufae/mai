using GLib;
using Json;

public class MCPClient : GLib.Object {
    private Subprocess? subprocess;
    private OutputStream? stdin_stream;
    private DataInputStream? stdout_stream;
    private int request_id = 1;
    private bool use_headers = false;

    public signal void message_received (string message);

    public async bool initialize () {
        try {
            spawn_process ();
            var caps = new Json.Object ();
            var params = new Json.Object ();
            params.set_string_member ("protocolVersion", "2024-11-05");
            params.set_object_member ("capabilities", caps);
            var request = create_request ("initialize", params);
            var response = yield send_request (request);
            if (response.get_string_member ("error") != null) {
                return false;
            }
            // Send initialized notification
            var notification = create_notification ("notifications/initialized", null);
            yield send_notification (notification);
            return true;
        } catch (Error e) {
            return false;
        }
    }

    public async string call_tool (string name, HashTable<string, Value?> args) {
        var params = new Json.Object ();
        params.set_string_member ("name", name);
        var arguments = new Json.Object ();
        args.foreach ((key, val) => {
            if (val != null) {
                arguments.set_string_member (key, val.get_string ());
            }
        });
        params.set_object_member ("arguments", arguments);

        var request = create_request ("tools/call", params);
        var response = yield send_request (request);
        if (response.get_string_member ("error") != null) {
            return "Error: " + response.get_string_member ("error");
        }
        var result = response.get_object_member ("result");
        if (result != null) {
            var content = result.get_array_member ("content");
            if (content != null && content.get_length () > 0) {
                var first = content.get_object_element (0);
                if (first != null) {
                    return first.get_string_member ("text");
                }
            }
        }
        return "Response received";
    }

    private void spawn_process () throws Error {
        string[] argv = {"mai", "-M"};
        subprocess = new Subprocess.newv (argv, SubprocessFlags.STDIN_PIPE | SubprocessFlags.STDOUT_PIPE | SubprocessFlags.STDERR_PIPE);
        stdin_stream = subprocess.get_stdin_pipe ();
        stdout_stream = new DataInputStream (subprocess.get_stdout_pipe ());
    }

    private Json.Object create_request (string method, Json.Object? params) {
        var request = new Json.Object ();
        request.set_string_member ("jsonrpc", "2.0");
        request.set_int_member ("id", request_id++);
        request.set_string_member ("method", method);
        if (params != null) {
            request.set_object_member ("params", params);
        }
        return request;
    }

    private Json.Object create_notification (string method, Json.Object? params) {
        var notification = new Json.Object ();
        notification.set_string_member ("jsonrpc", "2.0");
        notification.set_string_member ("method", method);
        if (params != null) {
            notification.set_object_member ("params", params);
        }
        return notification;
    }

    private async Json.Object send_request (Json.Object request) {
        var generator = new Json.Generator ();
        var root = new Json.Node (Json.NodeType.OBJECT);
        root.set_object (request);
        generator.set_root (root);
        var data = generator.to_data (null) + "\n";
        yield write_to_stdin (data);
        // Wait for response
        var response_data = yield read_from_stdout ();
        var parser = new Json.Parser ();
        try {
            parser.load_from_data (response_data, -1);
        } catch (Error e) {
            // Handle error
        }
        return parser.get_root ().get_object ();
    }

    private async void send_notification (Json.Object notification) {
        var generator = new Json.Generator ();
        var root = new Json.Node (Json.NodeType.OBJECT);
        root.set_object (notification);
        generator.set_root (root);
        var data = generator.to_data (null) + "\n";
        yield write_to_stdin (data);
    }

    private async void write_to_stdin (string data) {
        try {
            yield stdin_stream.write_async (data.data);
        } catch (Error e) {
            // Handle error
        }
    }

    private async string read_from_stdout () {
        try {
            return yield stdout_stream.read_line_async ();
        } catch (Error e) {
            return "";
        }
    }
}
