defmodule Realtime.AuthTest do
  use ExUnit.Case, async: true

  alias Realtime.Auth
  alias Realtime.Test.Tokens

  describe "verify/2 accepts" do
    test "a ws token and returns its identity" do
      claims = Tokens.ws_claims(%{"role" => "admin"})
      assert {:ok, identity} = Auth.verify(Tokens.sign(claims))

      assert identity == %{
               kind: :user,
               sub: claims["sub"],
               wid: claims["wid"],
               role: "admin",
               name: "Anna",
               avatar: "https://example.test/a.png",
               teams: [],
               brand: nil,
               sid: claims["sid"],
               issued_ms: claims["iat_ms"],
               workspaces: %{claims["wid"] => %{role: "admin", teams: []}}
             }
    end

    test "the issue time from iat when a token has no iat_ms" do
      claims = Tokens.ws_claims() |> Map.delete("iat_ms")
      assert {:ok, %{issued_ms: ms}} = Auth.verify(Tokens.sign(claims))
      assert ms == claims["iat"] * 1000

      claims = Tokens.widget_claims()
      assert {:ok, %{issued_ms: ms, sid: nil}} = Auth.verify(Tokens.sign(claims))
      assert ms == claims["iat"] * 1000
    end

    test "a ws token without a session (an API key's)" do
      claims = Tokens.ws_claims(%{"kind" => "api_key"}) |> Map.delete("sid")
      assert {:ok, %{kind: :api_key, sid: nil}} = Auth.verify(Tokens.sign(claims))
    end

    test "a ws token's private teams, normalized" do
      [a, b] = [Tokens.uuid(), Tokens.uuid()]
      claims = Tokens.ws_claims(%{"teams" => [String.upcase(a), b, a]})
      assert {:ok, %{teams: [^a, ^b]}} = Auth.verify(Tokens.sign(claims))
    end

    test "an api_key ws token" do
      assert {:ok, %{kind: :api_key}} =
               Auth.verify(Tokens.sign(Tokens.ws_claims(%{"kind" => "api_key"})))
    end

    test "a person's other workspaces, normalized, with the token's own one" do
      [b, team] = [Tokens.uuid(), Tokens.uuid()]

      claims =
        Tokens.ws_claims(%{
          "workspaces" => %{
            String.upcase(b) => %{"role" => "viewer", "teams" => [String.upcase(team)]}
          }
        })

      assert {:ok, %{workspaces: map} = identity} = Auth.verify(Tokens.sign(claims))

      assert map == %{
               claims["wid"] => %{role: "agent", teams: []},
               b => %{role: "viewer", teams: [team]}
             }

      assert Auth.workspace(identity, b) == %{role: "viewer", teams: [team]}
      assert Auth.workspace(identity, Tokens.uuid()) == nil
    end

    test "the token's own workspace over a workspaces entry for it" do
      claims = Tokens.ws_claims(%{"role" => "viewer"})

      claims =
        Map.put(claims, "workspaces", %{
          claims["wid"] => %{"role" => "owner", "teams" => [Tokens.uuid()]}
        })

      assert {:ok, %{workspaces: %{} = map}} = Auth.verify(Tokens.sign(claims))
      assert map[claims["wid"]] == %{role: "viewer", teams: []}
    end

    test "a widget_ws token as a contact" do
      claims = Tokens.widget_claims()

      assert {:ok, %{kind: :contact, sub: sub, wid: wid, role: nil}} =
               Auth.verify(Tokens.sign(claims))

      assert {sub, wid} == {claims["sub"], claims["wid"]}
    end

    test "a widget_ws token's brand, normalized" do
      brand = Tokens.uuid()
      claims = Tokens.widget_claims(%{"bid" => String.upcase(brand)})
      assert {:ok, %{kind: :contact, brand: ^brand}} = Auth.verify(Tokens.sign(claims))
      assert {:ok, %{brand: nil}} = Auth.verify(Tokens.sign(Tokens.widget_claims()))
    end

    test "uppercase ids, normalized to lowercase" do
      claims = Tokens.ws_claims()
      upper = %{claims | "sub" => String.upcase(claims["sub"])}
      assert {:ok, %{sub: sub}} = Auth.verify(Tokens.sign(upper))
      assert sub == claims["sub"]
    end
  end

  describe "verify/2 refuses" do
    test "a malformed brand claim" do
      for bid <- ["nope", 1, [Tokens.uuid()]] do
        token = Tokens.sign(Tokens.widget_claims(%{"bid" => bid}))
        assert Auth.verify(token) == {:error, :invalid_claims}, inspect(bid)
      end
    end

    test "a malformed sid claim" do
      for sid <- ["nope", 1, ""] do
        token = Tokens.sign(Tokens.ws_claims(%{"sid" => sid}))
        assert Auth.verify(token) == {:error, :invalid_claims}, inspect(sid)
      end
    end

    test "a malformed workspaces claim, or one on an API key's token" do
      many_workspaces = for _ <- 1..51, into: %{}, do: {Tokens.uuid(), %{"role" => "agent"}}

      for {workspaces, extra} <- [
            {[Tokens.uuid()], %{}},
            {%{"nope" => %{"role" => "agent"}}, %{}},
            {%{Tokens.uuid() => %{"role" => "god"}}, %{}},
            {%{Tokens.uuid() => %{"teams" => []}}, %{}},
            {%{Tokens.uuid() => "agent"}, %{}},
            {%{Tokens.uuid() => %{"role" => "agent", "teams" => ["nope"]}}, %{}},
            {many_workspaces, %{}},
            {%{Tokens.uuid() => %{"role" => "agent"}}, %{"kind" => "api_key"}}
          ] do
        token = Tokens.sign(Tokens.ws_claims(Map.put(extra, "workspaces", workspaces)))
        assert Auth.verify(token) == {:error, :invalid_claims}, inspect(workspaces)
      end
    end

    test "a malformed teams claim" do
      for teams <- ["#{Tokens.uuid()}", [Tokens.uuid(), "nope"], [1], %{"a" => 1}] do
        token = Tokens.sign(Tokens.ws_claims(%{"teams" => teams}))
        assert Auth.verify(token) == {:error, :invalid_claims}, inspect(teams)
      end
    end

    test "every other purpose" do
      for purpose <- ["access", "mfa", "oauth_state", "", nil, 1] do
        token = Tokens.sign(Tokens.ws_claims(%{"purpose" => purpose}))
        assert Auth.verify(token) == {:error, :wrong_purpose}, inspect(purpose)
      end

      assert Auth.verify(Tokens.sign(Map.delete(Tokens.ws_claims(), "purpose"))) ==
               {:error, :wrong_purpose}
    end

    test "alg none" do
      assert Auth.verify(Tokens.unsigned(Tokens.ws_claims())) == {:error, :invalid_token}
    end

    test "HS384 and HS512 even with the right secret" do
      for alg <- ["HS384", "HS512"] do
        assert Auth.verify(Tokens.sign(Tokens.ws_claims(), alg: alg)) == {:error, :invalid_token}
      end
    end

    test "an asymmetric alg" do
      jwk = JOSE.JWK.generate_key({:ec, "P-256"})

      {_, token} =
        JOSE.JWT.sign(jwk, %{"alg" => "ES256"}, Tokens.ws_claims()) |> JOSE.JWS.compact()

      assert Auth.verify(token) == {:error, :invalid_token}
    end

    test "a wrong signature" do
      token = Tokens.sign(Tokens.ws_claims(), secret: String.duplicate("x", 40))
      assert Auth.verify(token) == {:error, :invalid_token}
    end

    test "an expired token" do
      token = Tokens.sign(Tokens.ws_claims(%{"exp" => Tokens.now() - 1}))
      assert Auth.verify(token) == {:error, :expired}

      token = Tokens.sign(Tokens.ws_claims(%{"exp" => Tokens.now()}))
      assert Auth.verify(token) == {:error, :expired}
    end

    test "a token without exp, or with a non-integer exp" do
      assert Auth.verify(Tokens.sign(Map.delete(Tokens.ws_claims(), "exp"))) ==
               {:error, :invalid_claims}

      assert Auth.verify(Tokens.sign(Tokens.ws_claims(%{"exp" => "9999999999"}))) ==
               {:error, :invalid_claims}
    end

    test "a token that is not valid yet" do
      token = Tokens.sign(Tokens.ws_claims(%{"nbf" => Tokens.now() + 60}))
      assert Auth.verify(token) == {:error, :invalid_token}
    end

    test "bad claims" do
      for bad <- [
            %{"sub" => "not-a-uuid"},
            %{"wid" => nil},
            %{"kind" => "admin"},
            %{"role" => "root"},
            %{"sub" => "../x"}
          ] do
        assert Auth.verify(Tokens.sign(Tokens.ws_claims(bad))) == {:error, :invalid_claims},
               inspect(bad)
      end

      assert Auth.verify(Tokens.sign(Tokens.widget_claims(%{"wid" => "x"}))) ==
               {:error, :invalid_claims}
    end

    test "missing, garbage and oversized tokens" do
      assert Auth.verify(nil) == {:error, :missing_token}
      assert Auth.verify("") == {:error, :missing_token}
      assert Auth.verify("a.b.c") == {:error, :invalid_token}
      assert Auth.verify(%{"x" => 1}) == {:error, :invalid_token}

      huge = Tokens.sign(Tokens.ws_claims(%{"pad" => String.duplicate("a", 9000)}))
      assert Auth.verify(huge) == {:error, :invalid_token}
    end
  end

  describe "secret" do
    test "under 32 bytes is refused" do
      short = String.duplicate("s", 31)
      assert Auth.validate_secret(short) == {:error, :secret_too_short}
      assert Auth.validate_secret(nil) == {:error, :secret_too_short}
      assert Auth.validate_secret(String.duplicate("s", 32)) == :ok

      # A token signed with a short secret does not verify even against itself.
      token = Tokens.sign(Tokens.ws_claims(), secret: short)
      assert Auth.verify(token, secret: short) == {:error, :invalid_token}
    end
  end
end
