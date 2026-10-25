defmodule Realtime.RevocationsTest do
  use ExUnit.Case, async: true

  alias Realtime.Revocations
  alias Realtime.Test.Tokens

  defp identity(issued, sid \\ nil), do: %{sub: Tokens.uuid(), sid: sid, issued_ms: issued}

  test "a token issued at or before a revocation of its subject is revoked, one after it is not" do
    now = Tokens.now_ms()
    id = identity(now)
    refute Revocations.revoked?(id)

    Revocations.record({:sub, id.sub}, now)
    assert Revocations.revoked?(id)
    assert Revocations.revoked?(%{id | issued_ms: now - 1})
    refute Revocations.revoked?(%{id | issued_ms: now + 1})
  end

  test "a session's revocation reaches that session's tokens only" do
    now = Tokens.now_ms()
    sid = Tokens.uuid()
    id = identity(now - 10, sid)

    Revocations.record({:sid, sid}, now)
    assert Revocations.revoked?(id)
    refute Revocations.revoked?(%{id | sid: Tokens.uuid()})
    refute Revocations.revoked?(%{id | sid: nil})
  end

  test "a later revocation of the same key wins over an earlier one" do
    now = Tokens.now_ms()
    id = identity(now)
    Revocations.record({:sub, id.sub}, now + 5)
    Revocations.record({:sub, id.sub}, now - 5)
    assert Revocations.revoked?(%{id | issued_ms: now + 5})
  end

  test "entries older than a token's lifetime are swept, newer ones kept" do
    mono = System.monotonic_time(:millisecond)
    now = Tokens.now_ms()
    old = identity(now)
    fresh = identity(now)

    Revocations.record({:sub, old.sub}, now, mono - Revocations.ttl_ms() - 1)
    Revocations.record({:sub, fresh.sub}, now, mono)
    assert Revocations.revoked?(old)

    Revocations.sweep(mono)
    refute Revocations.revoked?(old)
    assert Revocations.revoked?(fresh)
  end

  test "an entry outlives the token lifetime" do
    # Tokens live 5 minutes (auth.WSTTL); a token issued before a revocation must be
    # expired before the revocation is forgotten.
    assert Revocations.ttl_ms() > 300_000
  end
end
