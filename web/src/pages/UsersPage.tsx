import { useEffect, useState } from "react";

import { api, type User } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatusBadge } from "../components/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

export function UsersPage() {
  const [users, setUsers] = useState<User[]>([]);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("viewer");
  const [err, setErr] = useState<string | null>(null);

  const load = () =>
    api<{ users: User[] }>("/api/v1/users")
      .then((d) => setUsers(d.users || []))
      .catch((e: Error) => setErr(e.message));

  useEffect(() => {
    void load();
  }, []);

  const create = async () => {
    setErr(null);
    try {
      await api("/api/v1/users", {
        method: "POST",
        body: JSON.stringify({
          email,
          password,
          role,
          must_change_password: true,
        }),
      });
      setEmail("");
      setPassword("");
      await load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <>
      <PageHeader
        kicker="/ Users"
        title="Team access"
        description="Local accounts only. Create credentials here — no invite emails are sent."
      />
      <div className="grid gap-4 lg:grid-cols-[1.4fr_1fr]">
        <Card className="py-2">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={3}>
                    <EmptyState title="No users" />
                  </TableCell>
                </TableRow>
              ) : (
                users.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell>{u.email}</TableCell>
                    <TableCell>
                      <StatusBadge>{u.role}</StatusBadge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{u.created_at || ""}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Create user</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="user-email">Email</Label>
              <Input id="user-email" value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="user-pass">Temporary password</Label>
              <Input
                id="user-pass"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label>Role</Label>
              <Select value={role} onValueChange={setRole}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="viewer">viewer</SelectItem>
                  <SelectItem value="admin">admin</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <Button type="button" onClick={() => void create()}>
              Create user
            </Button>
            {err ? <p className="text-sm text-destructive">{err}</p> : null}
          </CardContent>
        </Card>
      </div>
    </>
  );
}
