using System;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;

// A loopback-only process fixture, never a Windows service or a production artifact.
class DeployFixture
{
    static void Main(string[] args)
    {
        string mode = "FIXTURE_MODE";
        string address = Environment.GetEnvironmentVariable("HTTP_ADDR");
        int port = 0;
        if (address != null) port = Int32.Parse(address.Substring(address.LastIndexOf(':') + 1));
        else
        {
            if (args.Length != 2 || args[0] != "-config") return;
            foreach (string line in File.ReadAllLines(args[1]))
                if (line.StartsWith("port: ")) port = Int32.Parse(line.Substring(6));
        }
        if (port == 0 || port == 8317) return;
        bool live = Path.GetFileName(AppDomain.CurrentDomain.BaseDirectory.TrimEnd('\\')) == "app";
        if (!live && Environment.GetEnvironmentVariable("CPAMP_DEPLOY_TEST_SENTINEL") != null) return;
        Console.WriteLine("fixture-private-stdout");
        Console.Error.WriteLine("fixture-private-stderr");
        if (mode == "broken" && live) return;
        TcpListener listener = new TcpListener(IPAddress.Loopback, port);
        listener.Start();
        while (true)
        {
            using (TcpClient client = listener.AcceptTcpClient())
            using (NetworkStream stream = client.GetStream())
            {
                byte[] request = new byte[8192];
                stream.Read(request, 0, request.Length);
                string body = "fixture-healthy " + mode;
                bool failStatus = Encoding.ASCII.GetString(request).StartsWith("GET /status503 ");
                byte[] bytes = Encoding.UTF8.GetBytes(body);
                string status = failStatus ? "503 Service Unavailable" : "200 OK";
                byte[] header = Encoding.ASCII.GetBytes("HTTP/1.1 " + status + "\r\nContent-Length: " + bytes.Length + "\r\nConnection: close\r\n\r\n");
                stream.Write(header, 0, header.Length);
                stream.Write(bytes, 0, bytes.Length);
            }
        }
    }
}
